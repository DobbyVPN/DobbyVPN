package mobilebinding

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"core/sessionapi"
)

type fakeLookup struct{ reply string }

func (f fakeLookup) ResolveUnderlying(string, int64, string, int64, string, int64) string {
	return f.reply
}
func (fakeLookup) CancelUnderlying(int64) {}
func (fakeLookup) FinishUnderlying(int64) {}

// A "no such host" answer from the Android resolver reaches the core as a
// negative answer, so it is never replaced by a remembered address.
func TestNativeResolverReportsNotFoundAsNegativeAnswer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ref := sessionapi.SessionRef{SessionID: "session", Generation: 1}
	resolve := newNativeUnderlyingResolver(fakeLookup{`{"notFound":true}`}, ref, 1, "network-a")
	_, err := resolve(ctx, "endpoint.example")
	var dnsErr *net.DNSError
	if !errors.As(err, &dnsErr) || !dnsErr.IsNotFound {
		t.Fatalf("want a not-found DNS error, got %v", err)
	}
	resolve = newNativeUnderlyingResolver(fakeLookup{`{"error":"Underlying DNS deadline expired"}`}, ref, 1, "network-a")
	if _, err := resolve(ctx, "endpoint.example"); err == nil || errors.As(err, &dnsErr) {
		t.Fatalf("a failed query must stay a plain failure, got %v", err)
	}
}
