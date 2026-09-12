package v2

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDefaultConfigLoaderAcceptsInlineAndURL(t *testing.T) {
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

func TestDefaultConfigLoaderRejectsUnsupportedScheme(t *testing.T) {
	if _, err := (DefaultConfigLoader{}).Load(context.Background(), []byte("ftp://example.invalid/config")); CodeOf(err) != FailureInvalidArgument {
		t.Fatalf("unsupported scheme error = %v", err)
	}
}

func TestDefaultConfigLoaderFollowsRedirects(t *testing.T) {
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
	} else if errors.Unwrap(err) == nil {
		t.Fatalf("canceled load lost its original cause: %v", err)
	}
}
