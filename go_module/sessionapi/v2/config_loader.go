package v2

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

func (l DefaultConfigLoader) Load(ctx context.Context, source []byte) (LoadedConfig, error) {
	if len(source) == 0 {
		return LoadedConfig{}, failure(FailureInvalidArgument, "configuration source is empty")
	}
	trimmed := strings.TrimSpace(string(source))
	if sourceSchemeRE.MatchString(trimmed) {
		parsed, err := url.Parse(trimmed)
		if err == nil && (strings.EqualFold(parsed.Scheme, "http") || strings.EqualFold(parsed.Scheme, "https")) && parsed.Host != "" {
			return l.loadURL(ctx, trimmed)
		}
		return LoadedConfig{}, failure(FailureInvalidArgument, "configuration URL must use HTTP or HTTPS")
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
	client := l.Client
	if client == nil {
		client = &http.Client{}
	}
	request.Header.Set("User-Agent", "DobbyVPN/"+versionOrDev(l.Version))
	response, err := client.Do(request)
	if err != nil {
		return LoadedConfig{}, failureWithCause(FailureInvalidArgument, "configuration URL could not be fetched", err)
	}
	body, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return LoadedConfig{}, failureWithCause(
			FailureInvalidArgument,
			"configuration URL returned a non-success response",
			errors.Join(fmt.Errorf("configuration URL returned HTTP %d", response.StatusCode), readErr, closeErr),
		)
	}
	if readErr != nil {
		return LoadedConfig{}, failureWithCause(FailureInvalidArgument, "configuration URL response could not be read", errors.Join(readErr, closeErr))
	}
	if closeErr != nil {
		return LoadedConfig{}, failureWithCause(FailureInvalidArgument, "configuration URL response could not be closed", closeErr)
	}
	return LoadedConfig{Raw: body, Kind: ConfigSourceURL}, nil
}

func versionOrDev(version string) string {
	if strings.TrimSpace(version) == "" {
		return "dev"
	}
	return version
}
