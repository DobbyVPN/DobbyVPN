package v2

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDefaultConfigLoaderAcceptsInlineAndURLWithoutLeakingSource(t *testing.T) {
	inline := []byte("[[Outline]]\nServer='vpn.invalid'\nPort=443\nPassword='secret'\n")
	loaded, err := (DefaultConfigLoader{}).Load(context.Background(), inline)
	if err != nil || loaded.Kind != ConfigSourceInline || string(loaded.Raw) != string(inline) {
		t.Fatalf("inline load = %#v, %v", loaded, err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("User-Agent"), "DobbyVPN/") {
			t.Errorf("User-Agent = %q", r.Header.Get("User-Agent"))
		}
		_, _ = w.Write(inline)
	}))
	defer server.Close()
	loaded, err = (DefaultConfigLoader{}).Load(context.Background(), []byte(server.URL))
	if err != nil || loaded.Kind != ConfigSourceURL || string(loaded.Raw) != string(inline) {
		t.Fatalf("URL load = %#v, %v", loaded, err)
	}
}

func TestDefaultConfigLoaderRejectsDowngradeAndOversize(t *testing.T) {
	if _, err := (DefaultConfigLoader{}).Load(context.Background(), []byte("ftp://example.invalid/config")); CodeOf(err) != FailureInvalidArgument {
		t.Fatalf("unsupported scheme error = %v", err)
	}
	if _, err := (DefaultConfigLoader{}).Load(context.Background(), []byte(strings.Repeat("x", maxConfigBytes+1))); CodeOf(err) != FailureInvalidArgument {
		t.Fatalf("oversize error = %v", err)
	}
	if _, err := (DefaultConfigLoader{}).Load(context.Background(), []byte("https://user:pass@example.invalid/config")); CodeOf(err) != FailureInvalidArgument {
		t.Fatalf("credential-bearing URL error = %v", err)
	}
}

func TestDefaultConfigLoaderLimitsRedirectsAndRejectsHTTPSDowngrade(t *testing.T) {
	var httpServer *httptest.Server
	httpServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/final" {
			_, _ = w.Write([]byte("[[Outline]]\nServer='vpn.invalid'\nPort=443\nPassword='secret'\n"))
			return
		}
		http.Redirect(w, r, httpServer.URL+"/final", http.StatusFound)
	}))
	defer httpServer.Close()
	if _, err := (DefaultConfigLoader{}).Load(context.Background(), []byte(httpServer.URL+"/start")); err != nil {
		t.Fatalf("HTTP redirect should be allowed: %v", err)
	}

	redirects := 0
	limitServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirects++
		http.Redirect(w, r, "/next", http.StatusFound)
	}))
	defer limitServer.Close()
	if _, err := (DefaultConfigLoader{}).Load(context.Background(), []byte(limitServer.URL)); CodeOf(err) != FailureInvalidArgument {
		t.Fatalf("redirect limit error = %v", err)
	}
	if redirects < 5 {
		t.Fatalf("redirects=%d, want bounded redirect attempts", redirects)
	}
	plainTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("[[Outline]]\nServer='vpn.invalid'\nPort=443\nPassword='secret'\n"))
	}))
	defer plainTarget.Close()
	tlsRedirect := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plainTarget.URL, http.StatusFound)
	}))
	defer tlsRedirect.Close()
	if _, err := (DefaultConfigLoader{Client: tlsRedirect.Client()}).Load(context.Background(), []byte(tlsRedirect.URL)); CodeOf(err) != FailureInvalidArgument {
		t.Fatalf("HTTPS downgrade error = %v", err)
	}
}

func TestDefaultConfigLoaderHonorsCallerCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := (DefaultConfigLoader{}).Load(ctx, []byte(server.URL)); CodeOf(err) != FailureInvalidArgument {
		t.Fatalf("canceled load error = %v", err)
	}
}
