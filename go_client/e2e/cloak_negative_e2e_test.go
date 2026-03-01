//go:build e2e

package e2e_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func buildCloakClientConfig(localPort int, uid, publicKey, serverName, remoteHost string, remotePort int) string {
	return strings.Join([]string{
		"Transport=direct",
		"ProxyMethod=e2e-http",
		"EncryptionMethod=plain",
		"UID=" + uid,
		"PublicKey=" + publicKey,
		"ServerName=" + serverName,
		"NumConn=1",
		"LocalHost=127.0.0.1",
		"LocalPort=" + strconv.Itoa(localPort),
		"RemoteHost=" + remoteHost,
		"RemotePort=" + strconv.Itoa(remotePort),
		"StreamTimeout=10",
	}, ";")
}

func runCloakClientAndWait(t *testing.T, config string, timeout time.Duration) (string, error) {
	t.Helper()
	cloakDir := filepath.Clean(filepath.Join("..", "..", "Cloak"))
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", "./cmd/ck-client", "-c", config, "-verbosity", "warning")
	cmd.Dir = cloakDir
	cmd.WaitDelay = 2 * time.Second
	out, err := cmd.CombinedOutput()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, exec.ErrWaitDelay) {
		return string(out), fmt.Errorf("cloak client did not fail fast within %s", timeout)
	}
	return string(out), err
}

func probeHTTPThroughCloak(localAddr string) (string, error) {
	conn, err := net.DialTimeout("tcp", localAddr, 2*time.Second)
	if err != nil {
		return "", err
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Write([]byte("GET / HTTP/1.1\r\nHost: e2e-http\r\nConnection: close\r\n\r\n")); err != nil {
		return "", err
	}

	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil && !strings.Contains(strings.ToLower(err.Error()), "eof") {
		return "", err
	}
	return string(buf[:n]), nil
}

func assertCloakConfigFailsE2E(t *testing.T, cfg string, localAddr string) {
	t.Helper()
	out, runErr := runCloakClientAndWait(t, cfg, 8*time.Second)
	if runErr == nil {
		t.Fatalf("expected cloak client to fail for invalid config, got success output=%q", out)
	}

	conn, err := net.DialTimeout("tcp", localAddr, 600*time.Millisecond)
	if err == nil {
		_ = conn.Close()
		body, reqErr := probeHTTPThroughCloak(localAddr)
		if reqErr == nil && (strings.Contains(body, "200 OK") || strings.Contains(body, "cloak-ok")) {
			t.Fatalf("expected cloak flow to fail for invalid config, got successful response: %q", body)
		}
		return
	}
	// Local endpoint should usually not appear for invalid config.
	// If it is unreachable, ensure process actually failed with a non-empty diagnostic.
	if strings.TrimSpace(out) == "" {
		t.Fatalf("expected non-empty failure diagnostics for invalid cloak config")
	}
}

func TestCloakInvalidUIDFailsViaDockerE2E(t *testing.T) {
	cloakHost := envOrDefault("E2E_CLOAK_HOST", "127.0.0.1")
	cloakPort := envIntOrDefault(t, "E2E_CLOAK_PORT", 18445)
	requireReachableEndpointOrSkip(t, net.JoinHostPort(cloakHost, strconv.Itoa(cloakPort)))

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to allocate local cloak client port: %v", err)
	}
	localPort := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()

	publicKey := envOrDefault("E2E_CLOAK_PUBLIC_KEY", "LWsatB8oVpTqOXFF2GK6ugW3wHhfutd5cuHGI6x57i4=")
	serverName := envOrDefault("E2E_CLOAK_SERVER_NAME", "www.bing.com")
	cfg := buildCloakClientConfig(localPort, "%%%invalid-uid%%%", publicKey, serverName, cloakHost, cloakPort)
	assertCloakConfigFailsE2E(t, cfg, net.JoinHostPort("127.0.0.1", strconv.Itoa(localPort)))
}

func TestCloakInvalidPublicKeyFailsViaDockerE2E(t *testing.T) {
	cloakHost := envOrDefault("E2E_CLOAK_HOST", "127.0.0.1")
	cloakPort := envIntOrDefault(t, "E2E_CLOAK_PORT", 18445)
	requireReachableEndpointOrSkip(t, net.JoinHostPort(cloakHost, strconv.Itoa(cloakPort)))

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to allocate local cloak client port: %v", err)
	}
	localPort := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()

	uid := envOrDefault("E2E_CLOAK_UID", "BvGSsQV96aNGhKh/GQ2A3A==")
	serverName := envOrDefault("E2E_CLOAK_SERVER_NAME", "www.bing.com")
	cfg := buildCloakClientConfig(localPort, uid, "@@@invalid-public-key@@@", serverName, cloakHost, cloakPort)
	assertCloakConfigFailsE2E(t, cfg, net.JoinHostPort("127.0.0.1", strconv.Itoa(localPort)))
}

func TestCloakInvalidRemoteHostFailsFastViaDockerE2E(t *testing.T) {
	cloakPort := envIntOrDefault(t, "E2E_CLOAK_PORT", 18445)
	// Ensure only harness is up; negative config intentionally uses invalid host.
	requireReachableEndpointOrSkip(t, net.JoinHostPort(envOrDefault("E2E_CLOAK_HOST", "127.0.0.1"), strconv.Itoa(cloakPort)))

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to allocate local cloak client port: %v", err)
	}
	localPort := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()

	uid := envOrDefault("E2E_CLOAK_UID", "BvGSsQV96aNGhKh/GQ2A3A==")
	publicKey := envOrDefault("E2E_CLOAK_PUBLIC_KEY", "LWsatB8oVpTqOXFF2GK6ugW3wHhfutd5cuHGI6x57i4=")
	serverName := envOrDefault("E2E_CLOAK_SERVER_NAME", "www.bing.com")
	cfg := buildCloakClientConfig(localPort, uid, publicKey, serverName, "invalid.invalid.host", cloakPort)
	assertCloakConfigFailsE2E(t, cfg, net.JoinHostPort("127.0.0.1", strconv.Itoa(localPort)))
}
