package protected_dialer

import (
	"context"
	"errors"
	"net"
	"testing"
)

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

	listener, err := net.Listen("tcp4", "127.0.0.1:0")
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
