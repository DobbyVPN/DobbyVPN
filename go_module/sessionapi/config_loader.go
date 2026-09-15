package sessionapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
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
	Raw  []byte
	Kind ConfigSourceKind
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

// configLoaderCause keeps an inspectable cause while preventing net/http errors
// from copying a subscription URL, credentials, or server response details
// into the public failure string.
type configLoaderCause struct {
	cause error
}

func (c configLoaderCause) Error() string { return "configuration fetch failed" }
func (c configLoaderCause) Unwrap() error { return c.cause }

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
		return LoadedConfig{}, failureWithCause(FailureInvalidArgument, "configuration URL is invalid", configLoaderCause{cause: err})
	}
	requestCtx, cancel := context.WithTimeout(request.Context(), configFetchTimeout)
	defer cancel()
	request = request.WithContext(requestCtx)
	client := &http.Client{}
	if l.Client != nil {
		copy := *l.Client
		client = &copy
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
			if err := callerCheckRedirect(req, via); err != nil {
				return err
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
	response, err := client.Do(request)
	if err != nil {
		return LoadedConfig{}, failureWithCause(FailureInvalidArgument, "configuration URL could not be fetched", configLoaderCause{cause: err})
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		closeErr := response.Body.Close()
		return LoadedConfig{}, failureWithCause(
			FailureInvalidArgument,
			"configuration URL returned a non-success response",
			configLoaderCause{cause: errors.Join(fmt.Errorf("configuration URL returned HTTP %d", response.StatusCode), closeErr)},
		)
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, maxConfigBytes+1))
	closeErr := response.Body.Close()
	if len(body) > maxConfigBytes {
		return LoadedConfig{}, failureWithCause(
			FailureInvalidArgument,
			"configuration URL response exceeds the 1 MiB size limit",
			configLoaderCause{cause: errors.Join(errors.New("configuration response exceeded the size limit"), readErr, closeErr)},
		)
	}
	if readErr != nil {
		return LoadedConfig{}, failureWithCause(FailureInvalidArgument, "configuration URL response could not be read", configLoaderCause{cause: errors.Join(readErr, closeErr)})
	}
	if closeErr != nil {
		return LoadedConfig{}, failureWithCause(FailureInvalidArgument, "configuration URL response could not be closed", configLoaderCause{cause: closeErr})
	}
	return LoadedConfig{Raw: body, Kind: ConfigSourceURL}, nil
}

func versionOrDev(version string) string {
	if strings.TrimSpace(version) == "" {
		return "dev"
	}
	return version
}
