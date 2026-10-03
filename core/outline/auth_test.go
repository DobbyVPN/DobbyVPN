package outline

import (
	"bufio"
	"context"
	"core/log"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"core/dnscache"
)

func TestOutlineSOCKSRequiresPerInstanceAuthentication(t *testing.T) {
	const config = "ss://Y2hhY2hhMjAtaWV0Zi1wb2x5MTMwNTp0ZXN0@127.0.0.1:443"
	var previous string
	for range 2 {
		device, err := NewOutlineDevice(config, dnscache.New())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if closeErr := device.Close(); closeErr != nil {
				t.Error(closeErr)
			}
		})
		endpoint, err := url.Parse("socks5://" + device.GetProxyAddr())
		if err != nil {
			t.Fatal(err)
		}
		password, ok := endpoint.User.Password()
		if !ok || len(password) < 16 || len(endpoint.User.Username()) < 16 {
			t.Fatalf("missing random credentials: %v", endpoint)
		}
		if previous == endpoint.User.String() {
			t.Fatal("credentials were reused")
		}
		previous = endpoint.User.String()
		t.Run("no-auth-rejected", func(t *testing.T) {
			conn := dialSOCKSTest(t, endpoint.Host)
			if _, err := conn.Write([]byte{5, 1, 0}); err != nil {
				t.Fatal(err)
			}
			reply := make([]byte, 2)
			if _, err := io.ReadFull(conn, reply); err != nil {
				t.Fatal(err)
			}
			if reply[0] != 5 || reply[1] != 255 {
				t.Fatalf("unauthenticated negotiation accepted: %v", reply)
			}
		})
		verifySOCKSAuthentication(t, endpoint, password, false)
		verifySOCKSAuthentication(t, endpoint, password, true)
	}
}

func dialSOCKSTest(t *testing.T, address string) net.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := conn.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	return conn
}

func verifySOCKSAuthentication(t *testing.T, endpoint *url.URL, password string, valid bool) {
	t.Helper()
	conn := dialSOCKSTest(t, endpoint.Host)
	if _, err := conn.Write([]byte{5, 1, 2}); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 2)
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatal(err)
	}
	if reply[1] != 2 {
		t.Fatalf("authentication method = %v", reply)
	}
	pass := password
	if !valid {
		pass = "incorrect"
	}
	request := append([]byte{1, byte(len(endpoint.User.Username()))}, endpoint.User.Username()...)
	request = append(request, byte(len(pass)))
	request = append(request, pass...)
	if _, err := conn.Write(request); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatal(err)
	}
	if (reply[1] == 0) != valid {
		t.Fatalf("authentication valid=%v reply=%v", valid, reply)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSOCKSLoggerDistinguishesRoutineMessagesFromFailures(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outline.jsonl")
	if err := log.SetPath(path); err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	boundary, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	logger := socksLogger{device: &OutlineDevice{}}
	logger.Errorf("client want to used addr %v, listen addr: %s", "0.0.0.0:0", "127.0.0.1:1234")
	logger.Errorf("server: %v", net.ErrClosed)
	logger.Errorf("server: %v", errors.New("connection failed"))
	logger.Errorf("server: %v", errors.New("chacha20poly1305: message authentication failed"))
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.Seek(boundary.Size(), io.SeekStart); err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(file)
	var levels []string
	for scanner.Scan() {
		var event map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatal(err)
		}
		message, _ := event["message"].(string)
		if !strings.Contains(message, "[SOCKS5 internal]") {
			continue
		}
		level, _ := event["level"].(string)
		levels = append(levels, level)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(levels, ","); got != "DEBUG,DEBUG,WARN,ERROR" {
		t.Fatalf("levels=%s", got)
	}
}
