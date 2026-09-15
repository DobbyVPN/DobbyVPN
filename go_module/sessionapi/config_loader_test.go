package sessionapi

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const loaderTestConfig = "[[Outline]]\nServer='vpn.invalid'\nPort=443\nPassword='synthetic'\n"

func TestDefaultConfigLoaderAcceptsInlineAndHTTPSURL(t *testing.T) {
	inline := []byte(loaderTestConfig)
	loaded, err := (DefaultConfigLoader{}).Load(context.Background(), inline)
	if err != nil || loaded.Kind != ConfigSourceInline || string(loaded.Raw) != string(inline) {
		t.Fatalf("inline load = %#v, %v", loaded, err)
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("User-Agent"), "DobbyVPN/") {
			t.Errorf("User-Agent = %q", r.Header.Get("User-Agent"))
		}
		_, _ = w.Write(inline)
	}))
	defer server.Close()
	client := server.Client()
	loaded, err = (DefaultConfigLoader{Client: client}).Load(context.Background(), []byte(server.URL))
	if err != nil || loaded.Kind != ConfigSourceURL || string(loaded.Raw) != string(inline) {
		t.Fatalf("HTTPS load = %#v, %v", loaded, err)
	}
	if client.CheckRedirect != nil {
		t.Fatal("loader mutated the caller's HTTP client")
	}
}

func TestDefaultConfigLoaderRejectsHTTPBeforeContact(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	defer server.Close()
	_, err := (DefaultConfigLoader{}).Load(context.Background(), []byte(server.URL))
	if CodeOf(err) != FailureInvalidArgument || !strings.Contains(err.Error(), "must use HTTPS") {
		t.Fatalf("HTTP URL error = %v", err)
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("HTTP server received %d requests", got)
	}
}

func TestDefaultConfigLoaderFollowsHTTPSRedirects(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/final" {
			_, _ = w.Write([]byte(loaderTestConfig))
			return
		}
		http.Redirect(w, r, "/final", http.StatusFound)
	}))
	defer server.Close()
	if _, err := (DefaultConfigLoader{Client: server.Client()}).Load(context.Background(), []byte(server.URL+"/start")); err != nil {
		t.Fatalf("HTTPS redirect failed: %v", err)
	}
}

func TestDefaultConfigLoaderRejectsHTTPSDowngrade(t *testing.T) {
	var targetRequests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetRequests.Add(1)
		_, _ = w.Write([]byte(loaderTestConfig))
	}))
	defer target.Close()
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer origin.Close()

	_, err := (DefaultConfigLoader{Client: origin.Client()}).Load(context.Background(), []byte(origin.URL))
	if CodeOf(err) != FailureInvalidArgument {
		t.Fatalf("downgrade error = %v", err)
	}
	if got := targetRequests.Load(); got != 0 {
		t.Fatalf("HTTP downgrade target received %d requests", got)
	}
	if strings.Contains(err.Error(), target.URL) {
		t.Fatalf("fetch error exposed redirect URL: %v", err)
	}
}

func TestDefaultConfigLoaderRespectsInjectedRedirectPolicy(t *testing.T) {
	var finalRequests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/final" {
			finalRequests.Add(1)
			_, _ = w.Write([]byte(loaderTestConfig))
			return
		}
		http.Redirect(w, r, "/final", http.StatusFound)
	}))
	defer server.Close()
	policyErr := errors.New("redirect denied")
	client := server.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return policyErr }

	_, err := (DefaultConfigLoader{Client: client}).Load(context.Background(), []byte(server.URL+"/start"))
	if CodeOf(err) != FailureInvalidArgument {
		t.Fatalf("restricted redirect error = %v", err)
	}
	if finalRequests.Load() != 0 {
		t.Fatal("injected redirect policy was ignored")
	}
	if client.CheckRedirect == nil {
		t.Fatal("loader removed caller redirect policy")
	}
}

func TestDefaultConfigLoaderRejectsDowngradeAfterInjectedRedirectPolicy(t *testing.T) {
	var targetRequests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetRequests.Add(1)
		_, _ = w.Write([]byte(loaderTestConfig))
	}))
	defer target.Close()

	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/next", http.StatusFound)
	}))
	defer origin.Close()

	client := origin.Client()
	client.CheckRedirect = func(req *http.Request, _ []*http.Request) error {
		targetURL, err := url.Parse(target.URL)
		if err != nil {
			return err
		}
		req.URL = targetURL
		return nil
	}
	_, err := (DefaultConfigLoader{Client: client}).Load(context.Background(), []byte(origin.URL))
	if CodeOf(err) != FailureInvalidArgument {
		t.Fatalf("mutated downgrade error = %v", err)
	}
	if got := targetRequests.Load(); got != 0 {
		t.Fatalf("HTTP downgrade target received %d requests", got)
	}
}

func TestDefaultConfigLoaderCapsRedirects(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Redirect(w, r, "/loop", http.StatusFound)
	}))
	defer server.Close()

	_, err := (DefaultConfigLoader{Client: server.Client()}).Load(context.Background(), []byte(server.URL))
	if CodeOf(err) != FailureInvalidArgument {
		t.Fatalf("redirect loop error = %v", err)
	}
	if got := requests.Load(); got > 11 {
		t.Fatalf("followed %d requests; expected at most 11 with ten redirects", got)
	}
}

func TestDefaultConfigLoaderEnforcesOneMiBForInlineAndURL(t *testing.T) {
	base := []byte(loaderTestConfig)
	exact := append(append([]byte(nil), base...), []byte("#"+strings.Repeat("x", maxConfigBytes-len(base)-1))...)
	if len(exact) != maxConfigBytes {
		t.Fatalf("test body size = %d", len(exact))
	}
	loaded, err := (DefaultConfigLoader{}).Load(context.Background(), exact)
	if err != nil || len(loaded.Raw) != maxConfigBytes {
		t.Fatalf("exact-limit inline load size=%d, error=%v", len(loaded.Raw), err)
	}
	if _, err := parseConfig(exact); err != nil {
		t.Fatalf("exact-limit parser rejected input: %v", err)
	}
	exactServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(exact)
	}))
	defer exactServer.Close()
	loaded, err = (DefaultConfigLoader{Client: exactServer.Client()}).Load(context.Background(), []byte(exactServer.URL))
	if err != nil || len(loaded.Raw) != maxConfigBytes {
		t.Fatalf("exact-limit URL load size=%d, error=%v", len(loaded.Raw), err)
	}
	tooLarge := append(exact, '#')
	if _, err := (DefaultConfigLoader{}).Load(context.Background(), tooLarge); CodeOf(err) != FailureInvalidArgument {
		t.Fatalf("oversized inline error = %v", err)
	}
	if _, err := parseConfig(tooLarge); CodeOf(err) != FailureMalformedConfig {
		t.Fatalf("oversized parser error = %v", err)
	}

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(tooLarge)
	}))
	defer server.Close()
	_, err = (DefaultConfigLoader{Client: server.Client()}).Load(context.Background(), []byte(server.URL))
	if CodeOf(err) != FailureInvalidArgument || !strings.Contains(err.Error(), "1 MiB") {
		t.Fatalf("oversized URL response error = %v", err)
	}
}

func TestDefaultConfigLoaderCapsChunkedAndCompressedResponses(t *testing.T) {
	for _, mode := range []string{"chunked", "compressed"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				payload := bytes.Repeat([]byte("x"), maxConfigBytes+1)
				if mode == "compressed" {
					w.Header().Set("Content-Encoding", "gzip")
					writer := gzip.NewWriter(w)
					_, _ = writer.Write(payload)
					_ = writer.Close()
					return
				}
				w.WriteHeader(http.StatusOK)
				w.(http.Flusher).Flush()
				_, _ = w.Write(payload)
			}))
			defer server.Close()
			_, err := (DefaultConfigLoader{Client: server.Client()}).Load(context.Background(), []byte(server.URL))
			if CodeOf(err) != FailureInvalidArgument || !strings.Contains(err.Error(), "1 MiB") {
				t.Fatalf("oversized %s response error = %v", mode, err)
			}
		})
	}
}

func TestDefaultConfigLoaderChecksStatusBeforeReadingAndClosesBodies(t *testing.T) {
	body := &trackedBody{data: []byte("sensitive response body")}
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusBadGateway, Body: body, Header: make(http.Header)}, nil
	})}
	secretURL := "https://user:password@example.invalid/private-token"
	_, err := (DefaultConfigLoader{Client: client}).Load(context.Background(), []byte(secretURL))
	if CodeOf(err) != FailureInvalidArgument {
		t.Fatalf("status error = %v", err)
	}
	if body.reads != 0 || !body.closed {
		t.Fatalf("body reads=%d closed=%v; status must be checked before reading and body closed", body.reads, body.closed)
	}
	for _, secret := range []string{"user", "password", "private-token", "sensitive response body"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("public error exposed %q: %v", secret, err)
		}
	}
	if errors.Unwrap(err) == nil {
		t.Fatalf("status error lost its cause: %v", err)
	}
}

func TestDefaultConfigLoaderClosesSuccessfulBody(t *testing.T) {
	body := &trackedBody{data: []byte(loaderTestConfig)}
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: body, Header: make(http.Header)}, nil
	})}
	loaded, err := (DefaultConfigLoader{Client: client}).Load(context.Background(), []byte("https://example.invalid/config"))
	if err != nil || string(loaded.Raw) != loaderTestConfig || !body.closed {
		t.Fatalf("load=%#v error=%v body closed=%v", loaded, err, body.closed)
	}
}

func TestDefaultConfigLoaderHonorsCallerCancellation(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := (DefaultConfigLoader{Client: server.Client()}).Load(ctx, []byte(server.URL))
	if CodeOf(err) != FailureInvalidArgument {
		t.Fatalf("canceled load error = %v", err)
	} else if errors.Unwrap(err) == nil {
		t.Fatalf("canceled load lost its original cause: %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

type trackedBody struct {
	data   []byte
	offset int
	reads  int
	closed bool
}

func (b *trackedBody) Read(p []byte) (int, error) {
	b.reads++
	if b.offset >= len(b.data) {
		return 0, io.EOF
	}
	n := copy(p, b.data[b.offset:])
	b.offset += n
	return n, nil
}

func (b *trackedBody) Close() error {
	b.closed = true
	return nil
}
