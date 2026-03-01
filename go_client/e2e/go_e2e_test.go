//go:build e2e

package e2e_test

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Jigsaw-Code/outline-sdk/transport"
	"github.com/Jigsaw-Code/outline-sdk/x/configurl"
	"github.com/gorilla/websocket"

	"go_client/common"
	"go_client/healthcheck"
	"go_client/tunnel"
)

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envIntOrDefault(t *testing.T, key string, fallback int) int {
	t.Helper()
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		t.Fatalf("invalid integer in %s=%q: %v", key, v, err)
	}
	return n
}

func requireReachableEndpointOrSkip(t *testing.T, address string) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", address, 500*time.Millisecond)
	if err != nil {
		t.Skipf("docker endpoint %s is unreachable: %v", address, err)
		return
	}
	_ = conn.Close()
}

func httpRoundtripOverDialer(t *testing.T, dialer transport.StreamDialer, targetAddr string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := dialer.DialStream(ctx, targetAddr)
	if err != nil {
		return "", err
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Write([]byte("GET / HTTP/1.1\r\nHost: e2e-http\r\nConnection: close\r\n\r\n")); err != nil {
		return "", err
	}

	buf := make([]byte, 2048)
	var response strings.Builder
	for {
		n, readErr := conn.Read(buf)
		if n > 0 {
			response.Write(buf[:n])
		}
		if readErr != nil {
			if strings.Contains(strings.ToLower(readErr.Error()), "eof") {
				break
			}
			return response.String(), readErr
		}
		if n == 0 {
			break
		}
	}
	return response.String(), nil
}

func buildShadowsocksDialer(t *testing.T, host string, port int, method, password string) transport.StreamDialer {
	t.Helper()
	ssAddr := net.JoinHostPort(host, strconv.Itoa(port))
	requireReachableEndpointOrSkip(t, ssAddr)

	creds := base64.RawURLEncoding.EncodeToString([]byte(method + ":" + password))
	sip002Config := fmt.Sprintf("ss://%s@%s", creds, ssAddr)

	providers := configurl.NewDefaultProviders()
	dialer, err := providers.NewStreamDialer(context.Background(), sip002Config)
	if err != nil {
		t.Fatalf("failed to build shadowsocks dialer from SIP002 config: %v", err)
	}
	return dialer
}

type mockTun struct {
	readCh  chan []byte
	writeCh chan []byte
	closed  chan struct{}
	once    sync.Once
}

func newMockTun() *mockTun {
	return &mockTun{
		readCh:  make(chan []byte, 8),
		writeCh: make(chan []byte, 8),
		closed:  make(chan struct{}),
	}
}

func (m *mockTun) Read(p []byte) (int, error) {
	select {
	case <-m.closed:
		return 0, io.EOF
	case data := <-m.readCh:
		n := copy(p, data)
		return n, nil
	case <-time.After(5 * time.Millisecond):
		return 0, io.EOF
	}
}

func (m *mockTun) Write(p []byte) (int, error) {
	cp := make([]byte, len(p))
	copy(cp, p)
	select {
	case <-m.closed:
		return 0, io.EOF
	case m.writeCh <- cp:
		return len(p), nil
	}
}

func (m *mockTun) Close() error {
	m.once.Do(func() { close(m.closed) })
	return nil
}

func (m *mockTun) injectRead(data []byte) {
	cp := make([]byte, len(data))
	copy(cp, data)
	m.readCh <- cp
}

func (m *mockTun) awaitWrite(timeout time.Duration) ([]byte, bool) {
	select {
	case data := <-m.writeCh:
		return data, true
	case <-time.After(timeout):
		return nil, false
	}
}

type mockVPNClient struct {
	connectCalls    atomic.Int32
	disconnectCalls atomic.Int32
	refreshCalls    atomic.Int32
}

type fnVPNClient struct {
	connectFn    func() error
	disconnectFn func() error
	refreshFn    func() error
}

func (f *fnVPNClient) Connect() error {
	if f.connectFn == nil {
		return nil
	}
	return f.connectFn()
}

func (f *fnVPNClient) Disconnect() error {
	if f.disconnectFn == nil {
		return nil
	}
	return f.disconnectFn()
}

func (f *fnVPNClient) Refresh() error {
	if f.refreshFn == nil {
		return nil
	}
	return f.refreshFn()
}

func (m *mockVPNClient) Connect() error {
	m.connectCalls.Add(1)
	return nil
}

func (m *mockVPNClient) Disconnect() error {
	m.disconnectCalls.Add(1)
	return nil
}

func (m *mockVPNClient) Refresh() error {
	m.refreshCalls.Add(1)
	return nil
}

type blockingVPNClient struct {
	connectCalls atomic.Int32
	startedCh    chan struct{}
	releaseCh    chan struct{}
}

func (m *blockingVPNClient) Connect() error {
	m.connectCalls.Add(1)
	select {
	case <-m.startedCh:
	default:
		close(m.startedCh)
	}
	<-m.releaseCh
	return nil
}

func (m *blockingVPNClient) Disconnect() error {
	return nil
}

func (m *blockingVPNClient) Refresh() error {
	return nil
}

func startSilentTCPServer(t *testing.T) (host string, port int, stop func()) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
				buf := make([]byte, 1)
				_, _ = c.Read(buf)
				time.Sleep(1500 * time.Millisecond)
			}(conn)
		}
	}()

	addr := ln.Addr().(*net.TCPAddr)
	return "127.0.0.1", addr.Port, func() {
		_ = ln.Close()
		<-done
	}
}

// Verifies tunnel data path forwards packets in both directions and stops cleanly.
func TestTunnelBidirectionalTransferE2E(t *testing.T) {
	tunnel.StopTransfer()
	tun := newMockTun()
	defer func() {
		_ = tun.Close()
		tunnel.StopTransfer()
	}()

	outboundCh := make(chan []byte, 1)
	var emitted atomic.Bool
	inboundPayload := []byte{9, 8, 7}
	outboundPayload := []byte{1, 2, 3, 4}

	tunnel.StartTransfer(
		tun,
		func(b []byte) (int, error) {
			if emitted.CompareAndSwap(false, true) {
				copy(b, inboundPayload)
				return len(inboundPayload), nil
			}
			return 0, io.EOF
		},
		func(packet []byte) (int, error) {
			cp := make([]byte, len(packet))
			copy(cp, packet)
			outboundCh <- cp
			return len(packet), nil
		},
	)

	tun.injectRead(outboundPayload)

	select {
	case got := <-outboundCh:
		if string(got) != string(outboundPayload) {
			t.Fatalf("unexpected outbound packet: got %v want %v", got, outboundPayload)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for outbound packet")
	}

	gotTunWrite, ok := tun.awaitWrite(1 * time.Second)
	if !ok {
		t.Fatal("timed out waiting for packet written to tun")
	}
	if string(gotTunWrite) != string(inboundPayload) {
		t.Fatalf("unexpected inbound packet: got %v want %v", gotTunWrite, inboundPayload)
	}
}

// Verifies TCP alive probe succeeds on live endpoint and fails on closed endpoint.
func TestHealthcheckTCPProbeE2E(t *testing.T) {
	host, port, stop := startSilentTCPServer(t)
	defer stop()

	if err := healthcheck.CheckServerAlive(host, port); err != nil {
		t.Fatalf("expected alive server, got error: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for closed port failed: %v", err)
	}
	closedPort := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	if err := healthcheck.CheckServerAlive("127.0.0.1", closedPort); err == nil {
		t.Fatal("expected error for closed port, got nil")
	}
}

// Verifies CommonClient executes connect-refresh-disconnect lifecycle for an active client.
func TestCommonClientLifecycleE2E(t *testing.T) {
	client := &common.CommonClient{}
	mock := &mockVPNClient{}
	client.SetVpnClient("outline", mock)

	if err := client.Connect("outline"); err != nil {
		t.Fatalf("connect failed: %v", err)
	}
	if err := client.RefreshAll(); err != nil {
		t.Fatalf("refresh all failed: %v", err)
	}
	if err := client.Disconnect("outline"); err != nil {
		t.Fatalf("disconnect failed: %v", err)
	}

	if mock.connectCalls.Load() != 1 {
		t.Fatalf("connect calls: got %d want 1", mock.connectCalls.Load())
	}
	if mock.refreshCalls.Load() != 1 {
		t.Fatalf("refresh calls: got %d want 1", mock.refreshCalls.Load())
	}
	if mock.disconnectCalls.Load() != 1 {
		t.Fatalf("disconnect calls: got %d want 1", mock.disconnectCalls.Load())
	}
}

// Verifies concurrent second Connect is skipped while first Connect is in critical section.
func TestCommonClientConcurrentConnectE2E(t *testing.T) {
	client := &common.CommonClient{}
	blocking := &blockingVPNClient{
		startedCh: make(chan struct{}),
		releaseCh: make(chan struct{}),
	}
	client.SetVpnClient("outline", blocking)

	firstDone := make(chan error, 1)
	go func() {
		firstDone <- client.Connect("outline")
	}()

	select {
	case <-blocking.startedCh:
	case <-time.After(1 * time.Second):
		t.Fatal("first connect did not enter critical section in time")
	}

	if err := client.Connect("outline"); err != nil {
		t.Fatalf("second connect returned error: %v", err)
	}
	if calls := blocking.connectCalls.Load(); calls != 1 {
		t.Fatalf("unexpected connect calls during contention: got %d want 1", calls)
	}

	close(blocking.releaseCh)
	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatalf("first connect failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting first connect completion")
	}
}

// Verifies RefreshAll touches only active clients and skips inactive ones.
func TestCommonClientRefreshOnlyActiveE2E(t *testing.T) {
	client := &common.CommonClient{}
	active := &mockVPNClient{}
	inactive := &mockVPNClient{}
	client.SetVpnClient("active", active)
	client.SetVpnClient("inactive", inactive)
	client.MarkActive("active")

	if err := client.RefreshAll(); err != nil {
		t.Fatalf("refresh all failed: %v", err)
	}

	if active.refreshCalls.Load() != 1 {
		t.Fatalf("active refresh calls: got %d want 1", active.refreshCalls.Load())
	}
	if inactive.refreshCalls.Load() != 0 {
		t.Fatalf("inactive refresh calls: got %d want 0", inactive.refreshCalls.Load())
	}
}

// Verifies StopTransfer remains safe when called repeatedly after active transfer.
func TestTunnelStopTransferIdempotentE2E(t *testing.T) {
	tunnel.StopTransfer()
	tun := newMockTun()
	defer func() {
		_ = tun.Close()
		tunnel.StopTransfer()
	}()

	tunnel.StartTransfer(tun, nil, nil)
	tunnel.StopTransfer()
	tunnel.StopTransfer()
	tunnel.StopTransfer()
}

// Verifies TCP alive probe fails fast for invalid destination address.
func TestHealthcheckInvalidAddressE2E(t *testing.T) {
	if err := healthcheck.CheckServerAlive("256.256.256.256", 443); err == nil {
		t.Fatal("expected error for invalid destination address, got nil")
	}
}

// Verifies URLTest succeeds against Docker-hosted HTTP endpoint.
func TestHealthcheckURLTestViaDockerE2E(t *testing.T) {
	host := envOrDefault("E2E_DOCKER_HOST", "127.0.0.1")
	port := envIntOrDefault(t, "E2E_DOCKER_HTTP_PORT", 18080)
	standard := envIntOrDefault(t, "E2E_URLTEST_STANDARD", 1)
	address := net.JoinHostPort(host, strconv.Itoa(port))
	requireReachableEndpointOrSkip(t, address)
	url := "http://" + address

	var (
		ms  int32
		err error
	)
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("URLTest panicked in dependency path: %v", r)
			}
		}()
		ms, err = healthcheck.URLTest(url, standard)
	}()
	if err != nil {
		t.Fatalf("URLTest failed against docker endpoint %s: %v", url, err)
	}
	if ms < 0 {
		t.Fatalf("expected non-negative latency, got %d", ms)
	}
}

// Verifies TCPPing succeeds against Docker-hosted TCP endpoint.
func TestHealthcheckTCPPingViaDockerE2E(t *testing.T) {
	host := envOrDefault("E2E_DOCKER_HOST", "127.0.0.1")
	port := envIntOrDefault(t, "E2E_DOCKER_HTTP_PORT", 18080)
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	requireReachableEndpointOrSkip(t, addr)

	ms, err := healthcheck.TCPPing(addr)
	if err != nil {
		t.Fatalf("TCPPing failed against docker endpoint %s: %v", addr, err)
	}
	if ms < 0 {
		t.Fatalf("expected non-negative latency, got %d", ms)
	}
}

// Verifies Shadowsocks stream dialer reaches Docker HTTP service through ss proxy.
func TestShadowsocksConnectViaDockerE2E(t *testing.T) {
	ssHost := envOrDefault("E2E_SS_HOST", "127.0.0.1")
	ssPort := envIntOrDefault(t, "E2E_SS_PORT", 18388)
	ssMethod := envOrDefault("E2E_SS_METHOD", "chacha20-ietf-poly1305")
	ssPassword := envOrDefault("E2E_SS_PASSWORD", "e2e-password")
	targetAddr := envOrDefault("E2E_SS_TARGET", "172.29.0.10:5678")

	dialer := buildShadowsocksDialer(t, ssHost, ssPort, ssMethod, ssPassword)

	body, reqErr := httpRoundtripOverDialer(t, dialer, targetAddr)
	if reqErr != nil {
		t.Fatalf("failed to round-trip through shadowsocks proxy to %s: %v", targetAddr, reqErr)
	}
	if !strings.Contains(body, "200 OK") || !strings.Contains(strings.ToLower(body), "\r\n\r\nok") {
		t.Fatalf("unexpected proxied response: %q", body)
	}
}

// Verifies Shadowsocks rejects traffic when client uses incorrect password.
func TestShadowsocksWrongPasswordFailsViaDockerE2E(t *testing.T) {
	ssHost := envOrDefault("E2E_SS_HOST", "127.0.0.1")
	ssPort := envIntOrDefault(t, "E2E_SS_PORT", 18388)
	ssMethod := envOrDefault("E2E_SS_METHOD", "chacha20-ietf-poly1305")
	targetAddr := envOrDefault("E2E_SS_TARGET", "172.29.0.10:5678")
	ssAddr := net.JoinHostPort(ssHost, strconv.Itoa(ssPort))
	requireReachableEndpointOrSkip(t, ssAddr)

	creds := base64.RawURLEncoding.EncodeToString([]byte(ssMethod + ":" + "wrong-password"))
	badConfig := fmt.Sprintf("ss://%s@%s", creds, ssAddr)
	providers := configurl.NewDefaultProviders()
	dialer, err := providers.NewStreamDialer(context.Background(), badConfig)
	if err != nil {
		t.Fatalf("failed to build bad-password shadowsocks dialer: %v", err)
	}

	body, reqErr := httpRoundtripOverDialer(t, dialer, targetAddr)
	if reqErr == nil {
		t.Fatalf("expected proxy failure with wrong password, got nil error and response: %q", body)
	}
	if strings.Contains(body, "200 OK") || strings.Contains(body, "ok") {
		t.Fatalf("expected no successful HTTP response with wrong password, got: %q", body)
	}
}

// Verifies CommonClient lifecycle works with a real Shadowsocks-backed transport client.
func TestOutlineLifecycleViaShadowsocksE2E(t *testing.T) {
	type shadowsocksLifecycleClient struct {
		dialer    transport.StreamDialer
		target    string
		connected bool
	}
	connect := func(c *shadowsocksLifecycleClient) error {
		_, err := httpRoundtripOverDialer(t, c.dialer, c.target)
		if err != nil {
			return err
		}
		c.connected = true
		return nil
	}
	refresh := func(c *shadowsocksLifecycleClient) error {
		if !c.connected || c.dialer == nil {
			return fmt.Errorf("client is not connected")
		}
		_, err := httpRoundtripOverDialer(t, c.dialer, c.target)
		return err
	}
	disconnect := func(c *shadowsocksLifecycleClient) error {
		c.connected = false
		return nil
	}

	ssHost := envOrDefault("E2E_SS_HOST", "127.0.0.1")
	ssPort := envIntOrDefault(t, "E2E_SS_PORT", 18388)
	ssMethod := envOrDefault("E2E_SS_METHOD", "chacha20-ietf-poly1305")
	ssPassword := envOrDefault("E2E_SS_PASSWORD", "e2e-password")
	targetAddr := envOrDefault("E2E_SS_TARGET", "172.29.0.10:5678")
	dialer := buildShadowsocksDialer(t, ssHost, ssPort, ssMethod, ssPassword)

	ssClient := &shadowsocksLifecycleClient{
		dialer: dialer,
		target: targetAddr,
	}

	client := &common.CommonClient{}
	client.SetVpnClient("ss-lifecycle", &fnVPNClient{
		connectFn:    func() error { return connect(ssClient) },
		disconnectFn: func() error { return disconnect(ssClient) },
		refreshFn:    func() error { return refresh(ssClient) },
	})

	if err := client.Connect("ss-lifecycle"); err != nil {
		t.Fatalf("expected connect to succeed: %v", err)
	}
	if !ssClient.connected {
		t.Fatal("expected lifecycle client to be connected after Connect")
	}
	if err := client.RefreshAll(); err != nil {
		t.Fatalf("expected refresh to succeed after connect: %v", err)
	}
	if err := client.Disconnect("ss-lifecycle"); err != nil {
		t.Fatalf("expected disconnect to succeed: %v", err)
	}
	if ssClient.connected {
		t.Fatal("expected lifecycle client to be disconnected")
	}
	if err := refresh(ssClient); err == nil {
		t.Fatal("expected refresh to fail after disconnect")
	}
}

// Verifies WSS connection to Docker echo endpoint can send and receive a message.
func TestWSSConnectViaDockerE2E(t *testing.T) {
	host := envOrDefault("E2E_WSS_HOST", "127.0.0.1")
	port := envIntOrDefault(t, "E2E_WSS_PORT", 18443)
	path := envOrDefault("E2E_WSS_PATH", "/ws")
	address := net.JoinHostPort(host, strconv.Itoa(port))
	requireReachableEndpointOrSkip(t, address)

	u := "wss://" + address + path
	dialer := websocket.Dialer{
		HandshakeTimeout: 3 * time.Second,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
	}

	conn, _, err := dialer.Dial(u, nil)
	if err != nil {
		t.Fatalf("failed to connect WSS endpoint %s: %v", u, err)
	}
	defer conn.Close()

	want := "e2e-wss"
	if err := conn.WriteMessage(websocket.TextMessage, []byte(want)); err != nil {
		t.Fatalf("failed to write websocket message: %v", err)
	}
	_, got, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("failed to read websocket message: %v", err)
	}
	if string(got) != want {
		t.Fatalf("unexpected websocket echo: got %q want %q", string(got), want)
	}
}

// Verifies WSS endpoint rejects handshake on an invalid websocket path.
func TestWSSInvalidPathFailsViaDockerE2E(t *testing.T) {
	host := envOrDefault("E2E_WSS_HOST", "127.0.0.1")
	port := envIntOrDefault(t, "E2E_WSS_PORT", 18443)
	address := net.JoinHostPort(host, strconv.Itoa(port))
	requireReachableEndpointOrSkip(t, address)

	u := "wss://" + address + "/wrong-path"
	dialer := websocket.Dialer{
		HandshakeTimeout: 3 * time.Second,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
	}
	conn, resp, err := dialer.Dial(u, nil)
	if err == nil {
		_ = conn.Close()
		t.Fatalf("expected handshake failure for invalid path %s", u)
	}
	if resp == nil {
		t.Fatalf("expected HTTP response for invalid path failure, got nil response and err=%v", err)
	}
	if resp.StatusCode != 404 {
		t.Fatalf("expected HTTP 404 for invalid path, got %d", resp.StatusCode)
	}
}

// Verifies Cloak client can connect to Docker Cloak server and proxy HTTP traffic.
func TestCloakConnectViaDockerE2E(t *testing.T) {
	cloakHost := envOrDefault("E2E_CLOAK_HOST", "127.0.0.1")
	cloakPort := envIntOrDefault(t, "E2E_CLOAK_PORT", 18445)
	targetMessage := envOrDefault("E2E_CLOAK_EXPECT_BODY", "cloak-ok")
	requireReachableEndpointOrSkip(t, net.JoinHostPort(cloakHost, strconv.Itoa(cloakPort)))

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to allocate local cloak client port: %v", err)
	}
	localPort := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()

	uid := envOrDefault("E2E_CLOAK_UID", "BvGSsQV96aNGhKh/GQ2A3A==")
	pubKey := envOrDefault("E2E_CLOAK_PUBLIC_KEY", "LWsatB8oVpTqOXFF2GK6ugW3wHhfutd5cuHGI6x57i4=")
	serverName := envOrDefault("E2E_CLOAK_SERVER_NAME", "www.bing.com")
	config := strings.Join([]string{
		"Transport=direct",
		"ProxyMethod=e2e-http",
		"EncryptionMethod=plain",
		"UID=" + uid,
		"PublicKey=" + pubKey,
		"ServerName=" + serverName,
		"NumConn=1",
		"LocalHost=127.0.0.1",
		"LocalPort=" + strconv.Itoa(localPort),
		"RemoteHost=" + cloakHost,
		"RemotePort=" + strconv.Itoa(cloakPort),
		"StreamTimeout=10",
	}, ";")

	cloakDir := filepath.Clean(filepath.Join("..", "..", "Cloak"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", "./cmd/ck-client", "-c", config, "-verbosity", "warning")
	cmd.Dir = cloakDir
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start cloak client process: %v", err)
	}
	defer func() {
		cancel()
		_ = cmd.Wait()
	}()

	localAddr := net.JoinHostPort("127.0.0.1", strconv.Itoa(localPort))
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		conn, dialErr := net.DialTimeout("tcp", localAddr, 500*time.Millisecond)
		if dialErr == nil {
			_ = conn.Close()
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	conn, err := net.DialTimeout("tcp", localAddr, 3*time.Second)
	if err != nil {
		t.Fatalf("cloak local endpoint did not become reachable (%s): %v", localAddr, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write([]byte("GET / HTTP/1.1\r\nHost: e2e-http\r\nConnection: close\r\n\r\n")); err != nil {
		t.Fatalf("failed to write request through cloak local endpoint: %v", err)
	}

	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil && !strings.Contains(strings.ToLower(err.Error()), "eof") {
		t.Fatalf("failed to read response through cloak local endpoint: %v", err)
	}
	body := string(buf[:n])
	if !strings.Contains(body, targetMessage) {
		t.Fatalf("expected proxied response to contain %q, got %q", targetMessage, body)
	}
}
