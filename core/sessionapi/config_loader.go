package sessionapi

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"regexp"
	"strings"
	"time"

	"core/log"
)

const configFetchTimeout = 20 * time.Second

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
		if err == nil && strings.EqualFold(parsed.Scheme, "https") && parsed.Host != "" {
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
	callerCheckRedirect := client.CheckRedirect
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if !strings.EqualFold(req.URL.Scheme, "https") {
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
			if req.URL == nil || !strings.EqualFold(req.URL.Scheme, "https") {
				return errors.New("configuration redirect must use HTTPS")
			}
		}
		return nil
	}
	request.Header.Set("User-Agent", "DobbyVPN/"+versionOrDev(l.Version))
	fetchStartedAt := time.Now()
	tracePhase := func(phase string, err error) {
		fields := map[string]any{
			"phase":      phase,
			"source_url": source,
			"elapsed_ms": time.Since(fetchStartedAt).Milliseconds(),
		}
		if err != nil {
			fields["error"] = err.Error()
		}
		log.Debug("CONFIGURATION", "configuration URL HTTP fetch timing", fields)
	}
	requestTrace := &httptrace.ClientTrace{
		ConnectStart: func(string, string) {
			tracePhase("connect_start", nil)
		},
		ConnectDone: func(_ string, _ string, err error) {
			tracePhase("connect_done", err)
		},
		TLSHandshakeStart: func() {
			tracePhase("tls_handshake_start", nil)
		},
		TLSHandshakeDone: func(_ tls.ConnectionState, err error) {
			tracePhase("tls_handshake_done", err)
		},
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			tracePhase("wrote_request", info.Err)
		},
		GotFirstResponseByte: func() {
			tracePhase("first_response_byte", nil)
		},
	}
	requestCtx = httptrace.WithClientTrace(requestCtx, requestTrace)
	request = request.WithContext(requestCtx)
	tracePhase("fetch_start", nil)
	response, err := client.Do(request)
	tracePhase("fetch_return", err)
	if err != nil {
		return LoadedConfig{}, failureWithCause(FailureInvalidArgument, "configuration URL could not be fetched", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		closeErr := response.Body.Close()
		return LoadedConfig{}, failureWithCause(
			FailureInvalidArgument,
			"configuration URL returned a non-success response",
			errors.Join(fmt.Errorf("configuration URL returned HTTP %d", response.StatusCode), closeErr),
		)
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, maxConfigBytes+1))
	closeErr := response.Body.Close()
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
