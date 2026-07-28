//go:build darwin && amd64

package trusttunnel

import (
	"bufio"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
)

const testTrustTunnelConfig = `vpn_mode = "general"
[endpoint]
addresses = ["127.0.0.1:443"]
[listener.socks]
address = "127.0.0.1:1"
`

func TestRewriteTrustTunnelSOCKSConfigPreservesRoutingInput(t *testing.T) {
	rewritten, err := rewriteTrustTunnelSOCKSConfig(testTrustTunnelConfig, 18443, "test-user", "test-password")
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]interface{}
	if _, err := toml.Decode(rewritten, &parsed); err != nil {
		t.Fatal(err)
	}
	listener := parsed["listener"].(map[string]interface{})
	socks := listener["socks"].(map[string]interface{})
	if got := socks["address"]; got != "127.0.0.1:18443" {
		t.Fatalf("SOCKS address = %v", got)
	}
	if socks["username"] != "test-user" || socks["password"] != "test-password" {
		t.Fatal("generated SOCKS authentication was not applied")
	}

	routed, err := rewriteTrustTunnelRoutingConfig(rewritten, 51820, "en0")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(routed, "routing_table_id = 51820") || !strings.Contains(routed, "uplink_interface = \"en0\"") {
		t.Fatal("routing table and uplink were not retained in helper configuration")
	}
}

func TestValidateTrustTunnelHelperRejectsUnsafePaths(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trusttunnel_client")
	if err := os.WriteFile(path, []byte("test"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := validateTrustTunnelHelper(path); err != nil {
		t.Fatalf("safe helper rejected: %v", err)
	}
	if err := os.Chmod(path, 0o722); err != nil {
		t.Fatal(err)
	}
	if err := validateTrustTunnelHelper(path); err == nil {
		t.Fatal("group/world-writable helper accepted")
	}
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "helper-link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if err := validateTrustTunnelHelper(link); err == nil {
		t.Fatal("symlink helper accepted")
	}
}

func TestTrustTunnelOpenReadinessAndIdempotentClose(t *testing.T) {
	d := newTestTrustTunnelDevice(t, "socks")
	if err := d.Open(51820, "en0"); err != nil {
		t.Fatalf("Open = %v", err)
	}
	configDir := d.tempDir
	if configDir == "" {
		t.Fatal("Open did not create a private config directory")
	}
	assertMode(t, configDir, 0o700)
	assertMode(t, d.configPath, 0o600)
	if err := d.Close(); err != nil {
		t.Fatalf("first Close = %v", err)
	}
	if _, err := os.Stat(configDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("private config directory remains after Close: %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("second Close = %v", err)
	}
	if err := d.Open(51820, "en0"); err != nil {
		t.Fatalf("Open after Close = %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("Close after sequential reopen = %v", err)
	}
}

func TestTrustTunnelOpenReportsEarlyProcessExitAndCleansUp(t *testing.T) {
	d := newTestTrustTunnelDevice(t, "exit")
	if err := d.Open(51820, "en0"); err == nil {
		t.Fatal("Open succeeded after helper exit")
	}
	if d.tempDir != "" || d.configPath != "" {
		t.Fatal("failed Open retained private configuration artifacts")
	}
	if err := d.Close(); err != nil {
		t.Fatalf("Close after failed Open = %v", err)
	}
}

func newTestTrustTunnelDevice(t *testing.T, mode string) *TrustTunnelDevice {
	t.Helper()
	oldEnv := os.Getenv(trustTunnelHelperEnv)
	if err := os.Setenv(trustTunnelHelperEnv, os.Args[0]); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Setenv(trustTunnelHelperEnv, oldEnv) })
	oldCommand := trustTunnelCommand
	trustTunnelCommand = func(_ string, args ...string) *exec.Cmd {
		configPath := ""
		if len(args) == 2 && args[0] == "--config" {
			configPath = args[1]
		}
		cmd := exec.Command(os.Args[0], "-test.run=TestTrustTunnelFakeHelper", "--")
		cmd.Env = append(os.Environ(), "DOBBYVPN_FAKE_TRUSTTUNNEL="+mode, "DOBBYVPN_FAKE_CONFIG="+configPath)
		return cmd
	}
	t.Cleanup(func() { trustTunnelCommand = oldCommand })
	d, err := NewTrustTunnelDevice(testTrustTunnelConfig)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func assertMode(t *testing.T, path string, expected os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != expected {
		t.Fatalf("%s mode = %o, want %o", filepath.Base(path), got, expected)
	}
}

// TestTrustTunnelFakeHelper is an executable test-double. It verifies that the
// production code passes the official --config flag and serves the smallest
// authenticated SOCKS exchange required by the readiness check.
func TestTrustTunnelFakeHelper(t *testing.T) {
	if os.Getenv("DOBBYVPN_FAKE_TRUSTTUNNEL") == "" {
		return
	}
	if os.Getenv("DOBBYVPN_FAKE_TRUSTTUNNEL") == "exit" {
		os.Exit(0)
	}
	config, err := os.ReadFile(os.Getenv("DOBBYVPN_FAKE_CONFIG"))
	if err != nil {
		os.Exit(2)
	}
	var parsed struct {
		Listener struct {
			SOCKS struct {
				Address string `toml:"address"`
			} `toml:"socks"`
		} `toml:"listener"`
	}
	if _, err := toml.Decode(string(config), &parsed); err != nil || parsed.Listener.SOCKS.Address == "" {
		os.Exit(3)
	}
	listener, err := net.Listen("tcp", parsed.Listener.SOCKS.Address)
	if err != nil {
		os.Exit(4)
	}
	defer listener.Close()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-stop
		_ = listener.Close()
	}()
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		go fakeSOCKS(conn)
	}
}

func fakeSOCKS(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	reader := bufio.NewReader(conn)
	header := make([]byte, 3)
	if _, err := io.ReadFull(reader, header); err != nil {
		return
	}
	if _, err := conn.Write([]byte{5, 2}); err != nil {
		return
	}
	authHeader := make([]byte, 2)
	if _, err := io.ReadFull(reader, authHeader); err != nil {
		return
	}
	username := make([]byte, int(authHeader[1]))
	if _, err := io.ReadFull(reader, username); err != nil {
		return
	}
	passwordLen, err := reader.ReadByte()
	if err != nil {
		return
	}
	password := make([]byte, int(passwordLen))
	if _, err := io.ReadFull(reader, password); err != nil {
		return
	}
	_, _ = conn.Write([]byte{1, 0})
}
