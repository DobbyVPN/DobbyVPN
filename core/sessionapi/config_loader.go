package sessionapi

import (
	"context"
	"core/buildinfo"
	"core/log"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"regexp"
	"strings"
	"sync/atomic"
	"time"
)

const (
	configFetchTimeout    = 20 * time.Second
	configLogHost         = "host"
	configLogURL          = "url"
	configLogAttemptID    = "attempt_id"
	configHTTPScheme      = "https"
	configLogContextError = "context_error"
	configLogError        = "error"
)

var sourceSchemeRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*://`)

// ConfigSourceKind describes only how the caller supplied the source. The
// source value and acquired bytes stay inside the session manager.
type ConfigSourceKind string

const (
	ConfigSourceInline ConfigSourceKind = "INLINE"
	ConfigSourceURL    ConfigSourceKind = "URL"
)

type LoadedConfig struct {
	Raw       []byte
	Kind      ConfigSourceKind
	SourceURL string
}

// ConfigLoader is injectable so parser tests remain deterministic and do not
// require a network. Production uses DefaultConfigLoader.
type ConfigLoader interface {
	Load(context.Context, []byte) (LoadedConfig, error)
}

// DefaultConfigLoader accepts either inline configuration bytes or a URL.
// Files are read by the CLI and passed as inline bytes; GUI/mobile shells pass
// the user-entered URL without fetching it themselves.
type DefaultConfigLoader struct {
	Version string
	Client  *http.Client
}

func (l DefaultConfigLoader) Load(ctx context.Context, source []byte) (LoadedConfig, error) {
	if len(source) == 0 {
		return LoadedConfig{}, failure(FailureInvalidArgument, "configuration source is empty")
	}
	if len(source) > maxConfigBytes {
		return LoadedConfig{}, failure(FailureInvalidArgument, "configuration exceeds the 1 MiB size limit")
	}
	trimmed := strings.TrimSpace(string(source))
	if sourceSchemeRE.MatchString(trimmed) {
		parsed, err := url.Parse(trimmed)
		if err == nil && strings.EqualFold(parsed.Scheme, configHTTPScheme) && parsed.Host != "" {
			return l.loadURL(ctx, trimmed)
		}
		return LoadedConfig{}, failure(FailureInvalidArgument, "configuration URL must use HTTPS")
	}
	return LoadedConfig{Raw: append([]byte(nil), source...), Kind: ConfigSourceInline}, nil
}

func (l DefaultConfigLoader) loadURL(ctx context.Context, source string) (LoadedConfig, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source, http.NoBody)
	if err != nil {
		return LoadedConfig{}, failureWithCause(FailureInvalidArgument, "configuration URL is invalid", err)
	}
	requestCtx, cancel := context.WithTimeout(request.Context(), configFetchTimeout)
	defer cancel()
	request = request.WithContext(requestCtx)
	client := &http.Client{}
	if l.Client != nil {
		clientCopy := *l.Client
		client = &clientCopy
	}
	trace := &configFetchTrace{id: configFetchRequests.Add(1), started: time.Now()}
	trace.record("request started", map[string]any{configLogHost: request.URL.Host, configLogURL: source, "timeout_ms": configFetchTimeout.Milliseconds()})
	defer func() {
		trace.record("request ended", map[string]any{configLogContextError: errorText(requestCtx.Err())})
	}()
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	client.Transport = &configTraceTransport{base: transport, trace: trace}
	callerCheckRedirect := client.CheckRedirect
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		trace.record("redirect", map[string]any{configLogHost: req.URL.Host, configLogURL: req.URL.String(), "status": req.Response.StatusCode, "redirect_count": len(via)})
		if !strings.EqualFold(req.URL.Scheme, configHTTPScheme) {
			return errors.New("configuration redirect must use HTTPS")
		}
		if len(via) >= 10 {
			return errors.New("configuration URL stopped after 10 redirects")
		}
		if callerCheckRedirect != nil {
			redirectErr := callerCheckRedirect(req, via)
			if redirectErr != nil {
				return redirectErr
			}
			// The caller's policy callback receives the mutable redirect request.
			// Recheck afterward so it cannot turn an HTTPS redirect into HTTP.
			if req.URL == nil || !strings.EqualFold(req.URL.Scheme, configHTTPScheme) {
				return errors.New("configuration redirect must use HTTPS")
			}
		}
		return nil
	}
	version := l.Version
	if strings.TrimSpace(version) == "" {
		version = buildinfo.Version
	}
	request.Header.Set("User-Agent", "DobbyVPN/"+versionOrDev(version))
	response, err := client.Do(request)
	if err != nil {
		trace.record("request failed", map[string]any{configLogError: err.Error(), configLogContextError: errorText(requestCtx.Err())})
		return LoadedConfig{}, failureWithCause(FailureInvalidArgument, "configuration URL could not be fetched", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		drainedBytes, drainErr := io.Copy(io.Discard, io.LimitReader(response.Body, maxConfigBytes+1))
		if drainedBytes > maxConfigBytes {
			drainErr = errors.Join(
				drainErr,
				fmt.Errorf("configuration error response exceeded the %d-byte drain limit", maxConfigBytes),
			)
		}
		closeErr := response.Body.Close()
		trace.record("body completed", map[string]any{"bytes_received": drainedBytes, "read_error": errorText(drainErr), "close_error": errorText(closeErr), configLogContextError: errorText(requestCtx.Err()), "error_response": true})
		return LoadedConfig{}, failureWithCause(
			FailureInvalidArgument,
			"configuration URL returned a non-success response",
			errors.Join(fmt.Errorf("configuration URL returned HTTP %d", response.StatusCode), drainErr, closeErr),
		)
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, maxConfigBytes+1))
	closeErr := response.Body.Close()
	trace.record("body completed", map[string]any{"bytes_received": len(body), "read_error": errorText(readErr), "close_error": errorText(closeErr), configLogContextError: errorText(requestCtx.Err()), "size_limit_exceeded": len(body) > maxConfigBytes})
	if len(body) > maxConfigBytes {
		return LoadedConfig{}, failureWithCause(
			FailureInvalidArgument,
			"configuration URL response exceeds the 1 MiB size limit",
			errors.Join(errors.New("configuration response exceeded the size limit"), readErr, closeErr),
		)
	}
	if readErr != nil {
		return LoadedConfig{}, failureWithCause(FailureInvalidArgument, "configuration URL response could not be read", errors.Join(readErr, closeErr))
	}
	if closeErr != nil {
		return LoadedConfig{}, failureWithCause(FailureInvalidArgument, "configuration URL response could not be closed", closeErr)
	}
	return LoadedConfig{Raw: body, Kind: ConfigSourceURL, SourceURL: source}, nil
}

func versionOrDev(version string) string {
	if strings.TrimSpace(version) == "" {
		return "dev"
	}
	return version
}

// Each RoundTrip is a distinct HTTP attempt, including redirects. The transport
// wrapper attaches hooks only to its private request clone so redirect requests
// do not inherit and duplicate an earlier attempt's hooks.
var configFetchRequests atomic.Uint64
var configFetchAttempts atomic.Uint64

type configFetchTrace struct {
	id          uint64
	started     time.Time
	lastAttempt atomic.Uint64
}

func (t *configFetchTrace) record(message string, fields map[string]any) {
	fields["request_id"] = t.id
	if _, exists := fields[configLogAttemptID]; !exists {
		if id := t.lastAttempt.Load(); id != 0 {
			fields[configLogAttemptID] = id
		}
	}
	fields["elapsed_ms"] = time.Since(t.started).Milliseconds()
	log.Info("CONFIGURATION", "configuration fetch "+message, fields)
}

type configTraceTransport struct {
	base  http.RoundTripper
	trace *configFetchTrace
}

func (t *configTraceTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	id := configFetchAttempts.Add(1)
	t.trace.lastAttempt.Store(id)
	started := time.Now()
	var dns, connect, tlsSeen, gotConn, firstByte atomic.Bool
	record := func(phase string, fields map[string]any) {
		fields[configLogAttemptID] = id
		fields["attempt_elapsed_ms"] = time.Since(started).Milliseconds()
		t.trace.record(phase, fields)
	}
	hooks := &httptrace.ClientTrace{
		DNSStart: func(info httptrace.DNSStartInfo) {
			dns.Store(true)
			record("dns started", map[string]any{configLogHost: info.Host})
		},
		DNSDone: func(info httptrace.DNSDoneInfo) {
			dns.Store(true)
			record("dns completed", map[string]any{"addresses": info.Addrs, "coalesced": info.Coalesced, configLogError: errorText(info.Err)})
		},
		ConnectStart: func(network, address string) {
			connect.Store(true)
			record("connect started", map[string]any{"network": network, "address": address})
		},
		ConnectDone: func(network, address string, err error) {
			connect.Store(true)
			record("connect completed", map[string]any{"network": network, "address": address, configLogError: errorText(err)})
		},
		TLSHandshakeStart: func() { tlsSeen.Store(true); record("tls started", map[string]any{}) },
		TLSHandshakeDone: func(state tls.ConnectionState, err error) {
			tlsSeen.Store(true)
			record("tls completed", map[string]any{"version": state.Version, "protocol": state.NegotiatedProtocol, configLogError: errorText(err)})
		},
		GotConn: func(info httptrace.GotConnInfo) {
			gotConn.Store(true)
			record("connection selected", map[string]any{"local": info.Conn.LocalAddr().String(), "remote": info.Conn.RemoteAddr().String(), "reused": info.Reused, "was_idle": info.WasIdle})
		},
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			record("request written", map[string]any{configLogError: errorText(info.Err)})
		},
		GotFirstResponseByte: func() { firstByte.Store(true); record("first response byte", map[string]any{}) },
	}
	record("attempt started", map[string]any{configLogHost: request.URL.Host, configLogURL: request.URL.String()})
	traced := request.WithContext(httptrace.WithClientTrace(request.Context(), hooks))
	response, err := t.base.RoundTrip(traced)
	fields := map[string]any{configLogError: errorText(err), "dns_hooks": observed(dns.Load()), "connect_hooks": observed(connect.Load()), "tls_hooks": observed(tlsSeen.Load()), "connection_hook": observed(gotConn.Load()), "first_response_hook": observed(firstByte.Load()), configLogContextError: errorText(request.Context().Err())}
	if response != nil {
		fields["status"] = response.StatusCode
	}
	record("attempt completed", fields)
	return response, err
}

func observed(seen bool) string {
	if seen {
		return "observed"
	}
	return "unavailable_or_not_used"
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
