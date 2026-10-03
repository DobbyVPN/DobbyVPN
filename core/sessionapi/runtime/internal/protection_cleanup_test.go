//go:build (windows || darwin) && !(android || ios)

package internal

import (
	"context"
	"core/tunnel/protected_dialer"
	"errors"
	"fmt"
	"net"
	"testing"
)

// Opening fails before routes or a TUN are acquired. Socket binding still has
// to remain usable while the partially opened protocol releases its workers.
func TestProtectionSurvivesFailedProtocolCleanup(t *testing.T) {
	t.Cleanup(protected_dialer.ResetDefaultRoute)
	openFailure := errors.New("protocol partially opened")
	releaseFailure := errors.New("protocol workers still owned")
	attempts := 0
	device := &cleanupProtocol{openErr: openFailure, close: func() error {
		attempts++
		if err := protectedCleanupSocket(); err != nil {
			return fmt.Errorf("protocol lost socket protection before release: %w", err)
		}
		if attempts == 1 {
			return releaseFailure
		}
		return nil
	}}
	app := &App{ProtocolDevice: device, RoutingConfig: &RoutingConfig{}}
	initResult := make(chan error, 1)
	runErr := app.Run(context.Background(), initResult)
	if !errors.Is(runErr, openFailure) || !errors.Is(runErr, releaseFailure) {
		t.Fatalf("acquisition and cleanup errors: %v", runErr)
	}
	if err := protectedCleanupSocket(); err != nil {
		t.Fatalf("failed cleanup released protection: %v", err)
	}
	if err := app.Close(context.Background()); err != nil {
		t.Fatalf("cleanup retry: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("protocol release attempts = %d", attempts)
	}
	if err := protectedCleanupSocket(); !errors.Is(err, protected_dialer.ErrSocketProtectionUnavailable) {
		t.Fatalf("completed cleanup retained protection: %v", err)
	}
}

func protectedCleanupSocket() error {
	lc := net.ListenConfig{Control: protected_dialer.ProtectRawConn}
	conn, err := lc.ListenPacket(context.Background(), "udp4", "0.0.0.0:0")
	if err != nil {
		return err
	}
	return conn.Close()
}

type cleanupProtocol struct {
	openErr error
	close   func() error
}

func (p *cleanupProtocol) Open(int, string) error { return p.openErr }
func (p *cleanupProtocol) Close() error           { return p.close() }
func (*cleanupProtocol) GetServerIP() net.IP      { return net.IPv4(127, 0, 0, 1) }
func (*cleanupProtocol) GetProxyAddr() string     { return "" }
