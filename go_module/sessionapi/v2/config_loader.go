package v2

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const maxConfigBytes = 1 << 20

var sourceSchemeRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*://`)

// ConfigSourceKind describes only how the caller supplied the source. The
// source value and acquired bytes never cross the public result boundary.
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
	if len(source) == 0 || len(source) > maxConfigBytes {
		return LoadedConfig{}, failure(FailureInvalidArgument, "configuration source exceeds the 1 MiB limit")
	}
	trimmed := strings.TrimSpace(string(source))
	if sourceSchemeRE.MatchString(trimmed) {
		parsed, err := url.Parse(trimmed)
		if err == nil && (strings.EqualFold(parsed.Scheme, "http") || strings.EqualFold(parsed.Scheme, "https")) && parsed.Host != "" && parsed.User == nil {
			return l.loadURL(ctx, trimmed)
		}
		return LoadedConfig{}, failure(FailureInvalidArgument, "configuration URL must use HTTP or HTTPS")
	}
	return LoadedConfig{Raw: append([]byte(nil), source...), Kind: ConfigSourceInline}, nil
}

func (l DefaultConfigLoader) loadURL(ctx context.Context, source string) (LoadedConfig, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source, http.NoBody)
	if err != nil {
		return LoadedConfig{}, failure(FailureInvalidArgument, "configuration URL is invalid")
	}
	requestCtx, cancel := context.WithTimeout(request.Context(), 20*time.Second)
	defer cancel()
	request = request.WithContext(requestCtx)
	client := l.Client
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	clone := *client
	clone.CheckRedirect = func(next *http.Request, history []*http.Request) error {
		if len(history) >= 5 {
			return errors.New("redirect limit")
		}
		if len(history) > 0 && strings.EqualFold(history[len(history)-1].URL.Scheme, "https") && strings.EqualFold(next.URL.Scheme, "http") {
			return errors.New("https downgrade")
		}
		return nil
	}
	if clone.Timeout <= 0 || clone.Timeout > 20*time.Second {
		clone.Timeout = 20 * time.Second
	}
	request.Header.Set("User-Agent", "DobbyVPN/"+versionOrDev(l.Version))
	response, err := clone.Do(request)
	if err != nil {
		return LoadedConfig{}, failure(FailureInvalidArgument, "configuration URL could not be fetched")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return LoadedConfig{}, failure(FailureInvalidArgument, "configuration URL returned a non-success response")
	}
	limited := io.LimitReader(response.Body, maxConfigBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil || len(body) > maxConfigBytes {
		return LoadedConfig{}, failure(FailureInvalidArgument, "downloaded configuration exceeds the 1 MiB limit")
	}
	return LoadedConfig{Raw: body, Kind: ConfigSourceURL}, nil
}

func versionOrDev(version string) string {
	if strings.TrimSpace(version) == "" {
		return "dev"
	}
	return version
}
