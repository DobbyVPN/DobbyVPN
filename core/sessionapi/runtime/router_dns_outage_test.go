package runtime

import (
	"context"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"testing"
	"time"

	"core/dnscache"
	"core/protocol"
	"core/sessionapi"
	"core/tunnel"
	"core/tunnel/protected_dialer"

	M "github.com/xjasonlyu/tun2socks/v2/metadata"
)

// hangingRouterDNS answers normally until stop is called. After that it does
// not answer at all: every query hangs until its caller gives up, like a home
// router whose DNS stops answering for a minute.
type hangingRouterDNS struct {
	mu        sync.Mutex
	stopped   bool
	addresses map[string]string
}

func (r *hangingRouterDNS) stop() {
	r.mu.Lock()
	r.stopped = true
	r.mu.Unlock()
}

func (r *hangingRouterDNS) resolve(ctx context.Context, host string) ([]net.IPAddr, error) {
	r.mu.Lock()
	stopped, address := r.stopped, r.addresses[host]
	r.mu.Unlock()
	if stopped {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return []net.IPAddr{{IP: net.ParseIP(address)}}, nil
}

// outlineServerDevice connects to the VPN server through the real protected
// Outline dialer when it is created, as a protocol does, and reports the
// server address it reached.
type outlineServerDevice struct{ conn net.Conn }

func (d *outlineServerDevice) Open(int, string) error { return nil }
func (d *outlineServerDevice) GetProxyAddr() string   { return "127.0.0.1:1" }
func (d *outlineServerDevice) Close() error           { return d.conn.Close() }
func (d *outlineServerDevice) UpstreamIPs() []net.IP {
	return []net.IP{d.conn.RemoteAddr().(*net.TCPAddr).IP}
}

func outlineServerDeviceFactory(server string) DeviceFactory {
	return func(ctx context.Context, _ sessionapi.RuntimeProfile, cache *dnscache.Cache) (protocol.ProtocolDevice, error) {
		dialer, err := protected_dialer.NewOutlineProviders(ctx, cache, nil).NewStreamDialer(ctx, "")
		if err != nil {
			return nil, err
		}
		conn, err := dialer.DialStream(ctx, server)
		if err != nil {
			return nil, err
		}
		return &outlineServerDevice{conn: conn}, nil
	}
}

type idleCore struct{}

func (idleCore) Connect(context.Context) error    { return nil }
func (idleCore) Disconnect(context.Context) error { return nil }
func (idleCore) Attach(context.Context, uint64, string, *dnscache.Cache, *tunnel.BypassPolicy) error {
	return nil
}
func (idleCore) Detach(context.Context, uint64) error { return nil }
func (idleCore) ProtocolParameters() (int, string)    { return 0, "" }

// The router's DNS stops answering for a minute -> the VPN still connects to
// its server, and an ExcludeIPs host is not routed to a remembered old address.
func TestRouterDNSStopsAnsweringForAMinute(t *testing.T) {
	server, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	accepted := make(chan net.Conn, 4)
	go func() {
		for {
			conn, err := server.Accept()
			if err != nil {
				return
			}
			accepted <- conn
		}
	}()
	port := strconv.Itoa(server.Addr().(*net.TCPAddr).Port)

	router := &hangingRouterDNS{addresses: map[string]string{"vpn.example": "127.0.0.1", "nas.example": "192.0.2.20"}}
	cache := dnscache.New()
	t.Cleanup(cache.Close)
	cache.SetResolver("home-wifi", router.resolve)
	r := &runtime{
		options: Options{NewDevice: outlineServerDeviceFactory(net.JoinHostPort("vpn.example", port))},
		network: &networkOwner{ref: sessionapi.SessionRef{SessionID: "s", Generation: 1}, cache: cache, core: idleCore{}},
	}
	profile := sessionapi.RuntimeProfile{
		Config:       protocol.Config{OutlineURL: "ss://vpn.example"},
		ExcludeHosts: []string{"nas.example"},
	}
	nas := &M.Metadata{Network: M.TCP, DstIP: netip.MustParseAddr("192.0.2.20"), DstPort: 445}
	connect := func(generation uint64) *lease {
		t.Helper()
		started, err := r.startLocked(context.Background(), sessionapi.SessionRef{SessionID: "s", Generation: generation}, profile)
		if err != nil {
			t.Fatalf("connect %d: %v", generation, err)
		}
		t.Cleanup(func() { _ = started.Stop(context.Background()) })
		select {
		case conn := <-accepted:
			_ = conn.Close()
		case <-time.After(5 * time.Second):
			t.Fatalf("connect %d did not reach the server", generation)
		}
		return started
	}

	// The VPN connects while the router's DNS works: the ExcludeIPs host
	// bypasses the VPN.
	if first := connect(1); !first.bypass.IsBypass(nas) {
		t.Fatal("with working DNS the ExcludeIPs host must bypass the VPN")
	}

	router.stop()

	// The reconnect during the outage reaches the server's remembered address,
	// and the ExcludeIPs host stays in the VPN.
	second := connect(2)
	if second.bypass.IsBypass(nas) {
		t.Fatal("the ExcludeIPs host was routed outside the VPN to its remembered address")
	}
}
