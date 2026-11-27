//go:build linux && !android

package dnscache

import (
	"errors"
	"net"
	"testing"

	"github.com/godbus/dbus/v5"
)

func TestResolvedNegativeAnswers(t *testing.T) {
	for _, name := range []string{"org.freedesktop.resolve1.DnsError.NXDOMAIN", "org.freedesktop.resolve1.NoSuchRR"} {
		var dnsErr *net.DNSError
		err := resolvedNegativeAnswer("endpoint.example", dbus.Error{Name: name})
		if !errors.As(err, &dnsErr) || !dnsErr.IsNotFound {
			t.Fatalf("%s must be a negative answer, got %v", name, err)
		}
	}
	timeout := dbus.Error{Name: "org.freedesktop.resolve1.DnsError.SERVFAIL"}
	if err := resolvedNegativeAnswer("endpoint.example", timeout); !errors.As(err, new(dbus.Error)) {
		t.Fatalf("a resolver failure must stay a failure, got %v", err)
	}
}
