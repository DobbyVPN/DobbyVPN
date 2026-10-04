package outline

import (
	"context"
	"io"
	"net"
	"net/url"
	"sync"
	"testing"
	"time"

	"core/dnscache"

	"golang.getoutline.org/sdk/transport"
	"golang.org/x/net/proxy"
)

func TestCloseCancelsPendingSOCKSResolution(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	previous := net.DefaultResolver
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
		once.Do(func() { close(started) })
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-release:
			return nil, net.ErrClosed
		}
	}}
	defer func() { net.DefaultResolver = previous }()
	device, err := NewOutlineDevice("ss://Y2hhY2hhMjAtaWV0Zi1wb2x5MTMwNTp0ZXN0@127.0.0.1:443", dnscache.New())
	if err != nil {
		close(release)
		t.Fatal(err)
	}
	defer device.Close()
	defer close(release)
	endpoint, err := url.Parse("socks5://" + device.GetProxyAddr())
	if err != nil {
		t.Fatal(err)
	}
	password, _ := endpoint.User.Password()
	dialer, err := proxy.SOCKS5("tcp", endpoint.Host, &proxy.Auth{User: endpoint.User.Username(), Password: password}, &net.Dialer{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dialDone := make(chan error, 1)
	go func() {
		conn, dialErr := dialer.(proxy.ContextDialer).DialContext(ctx, "tcp", "pending.example.invalid:443")
		if conn != nil {
			_ = conn.Close()
		}
		dialDone <- dialErr
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("SOCKS request did not reach DNS resolution")
	}
	done := make(chan error, 1)
	go func() { done <- device.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close waited for DNS after stopping the tunnel")
	}
	select {
	case err := <-dialDone:
		if err == nil {
			t.Fatal("pending SOCKS request succeeded after Close")
		}
	case <-time.After(time.Second):
		t.Fatal("SOCKS request survived Close")
	}
}

type localStreamDialer struct{}

func (localStreamDialer) DialStream(ctx context.Context, address string) (transport.StreamConn, error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	return conn.(*net.TCPConn), nil
}

func TestCloseReleasesAuthenticatedTrafficAndIncompleteHandshake(t *testing.T) {
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	peerDone := make(chan error, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			peerDone <- acceptErr
			return
		}
		defer conn.Close()
		_, copyErr := io.Copy(conn, conn)
		peerDone <- copyErr
	}()
	device, err := NewOutlineDevice("ss://Y2hhY2hhMjAtaWV0Zi1wb2x5MTMwNTp0ZXN0@127.0.0.1:443", dnscache.New())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := device.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	device.streamDialer = localStreamDialer{}
	endpoint, err := url.Parse("socks5://" + device.GetProxyAddr())
	if err != nil {
		t.Fatal(err)
	}
	password, _ := endpoint.User.Password()
	dialer, err := proxy.SOCKS5("tcp", endpoint.Host, &proxy.Auth{User: endpoint.User.Username(), Password: password}, &net.Dialer{Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := dialer.(proxy.ContextDialer).DialContext(ctx, "tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte("echo")); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 4)
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatal(err)
	}
	if string(reply) != "echo" {
		t.Fatalf("SOCKS traffic = %q", reply)
	}
	waiting := dialSOCKSTest(t, endpoint.Host)
	done := make(chan error, 1)
	go func() { done <- device.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close left a SOCKS worker running")
	}
	if _, err := conn.Read(reply); err == nil {
		t.Fatal("authenticated connection survived Close")
	}
	if _, err := waiting.Read(reply); err == nil {
		t.Fatal("incomplete handshake survived Close")
	}
	select {
	case err := <-peerDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("outbound connection survived Close")
	}
}
