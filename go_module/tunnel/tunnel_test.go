package tunnel

import (
	"fmt"
	"net"
	"sync"
	"testing"
	"time"
)

func TestFlowLimiterAppliesGlobalAndDestinationUDPLimits(t *testing.T) {
	p := &DobbyProxy{limiter: newFlowLimiter()}

	var releases []func() int64
	for i := 0; i < maxActiveUDPAssociationsPerDest; i++ {
		_, release, err := p.reserveUDP("203.0.113.10:443")
		if err != nil {
			t.Fatalf("reserve UDP destination slot %d: %v", i, err)
		}
		releases = append(releases, release)
	}

	if _, _, err := p.reserveUDP("203.0.113.10:443"); err == nil {
		t.Fatal("expected UDP destination limit error")
	}

	for _, release := range releases {
		release()
	}
	releases = nil

	for i := 0; i < maxActiveUDPAssociations; i++ {
		_, release, err := p.reserveUDP(fmt.Sprintf("203.0.%d.%d:443", i/250, i%250+1))
		if err != nil {
			t.Fatalf("reserve UDP global slot %d: %v", i, err)
		}
		releases = append(releases, release)
	}

	if _, _, err := p.reserveUDP("198.51.100.1:443"); err == nil {
		t.Fatal("expected UDP global limit error")
	}

	for _, release := range releases {
		release()
	}

	if got := p.activeUDP.Load(); got != 0 {
		t.Fatalf("active UDP after release = %d, want 0", got)
	}
}

func TestFlowLimiterAppliesTCPHostLimit(t *testing.T) {
	p := &DobbyProxy{limiter: newFlowLimiter()}

	var releases []func() int64
	for i := 0; i < maxActiveTCPConnectionsPerHost; i++ {
		_, release, err := p.reserveTCP(fmt.Sprintf("203.0.113.10:%d", 10_000+i))
		if err != nil {
			t.Fatalf("reserve TCP host slot %d: %v", i, err)
		}
		releases = append(releases, release)
	}

	if _, _, err := p.reserveTCP("203.0.113.10:443"); err == nil {
		t.Fatal("expected TCP host limit error")
	}

	for _, release := range releases {
		release()
	}

	if got := p.activeTCP.Load(); got != 0 {
		t.Fatalf("active TCP after release = %d, want 0", got)
	}
}

func TestIdlePacketConnClosesAfterInactivity(t *testing.T) {
	underlying := newFakePacketConn()
	var idleTimeouts uint64
	conn := newIdlePacketConn(underlying, 20*time.Millisecond, "VPN", "203.0.113.10:443", func() uint64 {
		idleTimeouts++
		return idleTimeouts
	})
	defer conn.Close()

	select {
	case <-underlying.closed:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("idle packet connection did not close")
	}
	if idleTimeouts != 1 {
		t.Fatalf("idle timeout count = %d, want 1", idleTimeouts)
	}
}

func TestIdlePacketConnActivityExtendsDeadline(t *testing.T) {
	underlying := newFakePacketConn()
	conn := newIdlePacketConn(underlying, 40*time.Millisecond, "VPN", "203.0.113.10:443", nil)
	defer conn.Close()

	time.Sleep(25 * time.Millisecond)
	if _, err := conn.WriteTo([]byte("x"), fakeAddr("remote")); err != nil {
		t.Fatalf("write to idle packet conn: %v", err)
	}

	select {
	case <-underlying.closed:
		t.Fatal("idle packet connection closed before extended deadline")
	case <-time.After(25 * time.Millisecond):
	}

	select {
	case <-underlying.closed:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("idle packet connection did not close after extended deadline")
	}
}

type fakePacketConn struct {
	closed chan struct{}
	once   sync.Once
}

func newFakePacketConn() *fakePacketConn {
	return &fakePacketConn{closed: make(chan struct{})}
}

func (c *fakePacketConn) ReadFrom(_ []byte) (int, net.Addr, error) {
	return 0, nil, nil
}

func (c *fakePacketConn) WriteTo(b []byte, _ net.Addr) (int, error) {
	return len(b), nil
}

func (c *fakePacketConn) Close() error {
	c.once.Do(func() {
		close(c.closed)
	})
	return nil
}

func (c *fakePacketConn) LocalAddr() net.Addr {
	return fakeAddr("local")
}

func (c *fakePacketConn) SetDeadline(_ time.Time) error {
	return nil
}

func (c *fakePacketConn) SetReadDeadline(_ time.Time) error {
	return nil
}

func (c *fakePacketConn) SetWriteDeadline(_ time.Time) error {
	return nil
}

type fakeAddr string

func (a fakeAddr) Network() string {
	return "fake"
}

func (a fakeAddr) String() string {
	return string(a)
}
