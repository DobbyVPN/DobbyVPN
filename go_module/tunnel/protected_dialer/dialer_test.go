package protected_dialer

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"go_module/dnscache"
)

func TestDialersUsePreflightAddressWithoutDNS(t *testing.T) {
	dnscache.Clear()
	t.Cleanup(dnscache.Clear)
	if !dnscache.SetIPv4("vpn.invalid", "127.0.0.1", "test", time.Minute) {
		t.Fatal("could not set preflight address")
	}
	listenConfig := net.ListenConfig{}
	listener, err := listenConfig.Listen(context.Background(), "tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	address := net.JoinHostPort("vpn.invalid", port)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for name, dial := range map[string]func() (net.Conn, error){
		"tcp": func() (net.Conn, error) { return DialContextWithProtect(ctx, "tcp", address) },
		"udp": func() (net.Conn, error) { return DialUDPConnWithProtect(ctx, "udp", address) },
	} {
		t.Run(name, func(t *testing.T) {
			conn, dialErr := dial()
			if dialErr != nil {
				t.Fatal(dialErr)
			}
			if closeErr := conn.Close(); closeErr != nil {
				t.Fatal(closeErr)
			}
		})
	}
	packet, err := DialUDPWithProtect(ctx, "udp", address)
	if err != nil {
		t.Fatal(err)
	}
	if err := packet.Close(); err != nil {
		t.Fatal(err)
	}
	for _, unchanged := range []string{"uncached.invalid:443", "127.0.0.1:443", "[::1]:443", "invalid"} {
		if got := cachedDialAddress(unchanged); got != unchanged {
			t.Errorf("cachedDialAddress(%q) = %q", unchanged, got)
		}
	}
}

type failingProtector struct{ err error }

func (p failingProtector) Protect(uintptr, string) error { return p.err }

type recordingProtector struct{ calls int }

func (p *recordingProtector) Protect(uintptr, string) error {
	p.calls++
	return nil
}

func TestNonLoopbackTCPDialFailsWhenProtectionFails(t *testing.T) {
	original := protector
	t.Cleanup(func() { protector = original })
	protector = failingProtector{err: errors.New("VpnService.protect rejected socket")}

	_, err := DialContextWithProtect(context.Background(), "tcp", "192.0.2.1:443")
	if !errors.Is(err, ErrSocketProtectionUnavailable) {
		t.Fatalf("DialContextWithProtect error = %v, want socket protection error", err)
	}
}

func TestLoopbackTCPDialDoesNotRequireProtection(t *testing.T) {
	original := protector
	t.Cleanup(func() { protector = original })
	protector = failingProtector{err: errors.New("must not be called")}

	listenConfig := net.ListenConfig{}
	listener, err := listenConfig.Listen(context.Background(), "tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr == nil {
			_ = conn.Close()
		}
	}()

	conn, err := DialContextWithProtect(context.Background(), "tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("loopback dial returned protection error: %v", err)
	}
	_ = conn.Close()
}

func TestProtectSocketIntErrReportsNativeCallbackFailure(t *testing.T) {
	original := protector
	t.Cleanup(func() { protector = original })
	protector = failingProtector{err: errors.New("permission denied")}

	if err := ProtectSocketIntErr(17); !errors.Is(err, ErrSocketProtectionUnavailable) {
		t.Fatalf("ProtectSocketIntErr = %v, want socket protection error", err)
	}
}

func TestNonLoopbackUDPDialFailsWhenProtectionFails(t *testing.T) {
	original := protector
	t.Cleanup(func() { protector = original })
	protector = failingProtector{err: errors.New("VpnService.protect rejected socket")}

	_, err := DialUDPWithProtect(context.Background(), "udp", "192.0.2.1:53")
	if !errors.Is(err, ErrSocketProtectionUnavailable) {
		t.Fatalf("DialUDPWithProtect error = %v, want socket protection error", err)
	}
}

func TestNonLoopbackUDPConnectionFailsWhenProtectionFails(t *testing.T) {
	original := protector
	t.Cleanup(func() { protector = original })
	protector = failingProtector{err: errors.New("VpnService.protect rejected socket")}

	_, err := DialUDPConnWithProtect(context.Background(), "udp", "192.0.2.1:53")
	if !errors.Is(err, ErrSocketProtectionUnavailable) {
		t.Fatalf("DialUDPConnWithProtect error = %v, want socket protection error", err)
	}
}

func TestLoopbackUDPDialDoesNotRequireProtection(t *testing.T) {
	original := protector
	t.Cleanup(func() { protector = original })
	protector = failingProtector{err: errors.New("must not be called")}

	conn, err := DialUDPWithProtect(context.Background(), "udp", "127.0.0.1:53")
	if err != nil {
		t.Fatalf("loopback UDP dial returned protection error: %v", err)
	}
	defer conn.Close()
}

func TestNonLoopbackProtectionIsRequiredWhenNoPlatformProtectorExists(t *testing.T) {
	original := protector
	t.Cleanup(func() { protector = original })
	protector = nil

	if err := protectFD(99, networkTCP4, "198.51.100.10:443"); !errors.Is(err, ErrSocketProtectionUnavailable) {
		t.Fatalf("protectFD without protector = %v, want socket protection error", err)
	}
}

func TestProtectionReceivesNonLoopbackSockets(t *testing.T) {
	original := protector
	t.Cleanup(func() { protector = original })
	recording := &recordingProtector{}
	protector = recording

	if err := protectFD(42, networkTCP4, "198.51.100.10:443"); err != nil {
		t.Fatalf("protectFD() error = %v", err)
	}
	if recording.calls != 1 {
		t.Fatalf("Protect calls = %d, want 1", recording.calls)
	}
}
