package outline

import (
	"context"
	"io"
	"net"
	"net/url"
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
