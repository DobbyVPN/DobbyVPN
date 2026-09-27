package internal

import (
	"context"
	"errors"
	"net"
	"reflect"
	"syscall"
	"testing"
	"time"

	xraynet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/transport/internet"
)

func tcpDestination(t *testing.T, address string) xraynet.Destination {
	t.Helper()
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	port, err := xraynet.PortFromString(portText)
	if err != nil {
		t.Fatal(err)
	}
	return xraynet.TCPDestination(xraynet.ParseAddress(host), port)
}

func TestProtectedSystemDialerPreservesSourceAndProtectsBeforeSocketOptions(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr == nil {
			accepted <- conn
		}
	}()

	var events []string
	dialer := newProtectedSystemDialer(
		func(network, destination string, _ syscall.RawConn) error {
			events = append(events, "protect:"+network+":"+destination)
			return nil
		},
		func(network, address string, _ uintptr, config *internet.SocketConfig) error {
			if config == nil {
				t.Fatal("socket options callback received nil config")
			}
			events = append(events, "options:"+network+":"+address)
			return nil
		},
	)
	source := xraynet.IPAddress(net.ParseIP("127.0.0.2").To4())
	destination := tcpDestination(t, listener.Addr().String())
	conn, err := dialer.Dial(context.Background(), source, destination, &internet.SocketConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	local, ok := conn.LocalAddr().(*net.TCPAddr)
	if !ok || !local.IP.Equal(net.ParseIP("127.0.0.2")) {
		t.Fatalf("local source address = %v, want 127.0.0.2", conn.LocalAddr())
	}
	wantEvents := []string{
		"protect:tcp4:" + destination.NetAddr(),
		"options:tcp4:" + destination.NetAddr(),
	}
	if !reflect.DeepEqual(events, wantEvents) {
		t.Fatalf("socket setup order = %#v, want %#v", events, wantEvents)
	}
	select {
	case peer := <-accepted:
		_ = peer.Close()
	case <-time.After(time.Second):
		t.Fatal("listener did not accept protected connection")
	}
}

func TestProtectedSystemDialerReturnsProtectionErrorBeforeConnect(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	protectErr := errors.New("VpnService.protect rejected socket")
	optionsCalled := false
	dialer := newProtectedSystemDialer(
		func(string, string, syscall.RawConn) error { return protectErr },
		func(string, string, uintptr, *internet.SocketConfig) error {
			optionsCalled = true
			return nil
		},
	)
	destination := tcpDestination(t, listener.Addr().String())
	_, err = dialer.Dial(context.Background(), nil, destination, &internet.SocketConfig{})
	if !errors.Is(err, protectErr) {
		t.Fatalf("Dial error = %v, want protection error", err)
	}
	if optionsCalled {
		t.Fatal("socket options ran after protection failed")
	}
	if err := listener.(*net.TCPListener).SetDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if conn, acceptErr := listener.Accept(); acceptErr == nil {
		_ = conn.Close()
		t.Fatal("connection reached listener after protection failed")
	} else if timeout, ok := acceptErr.(net.Error); !ok || !timeout.Timeout() {
		t.Fatalf("Accept error = %v, want timeout without an incoming connection", acceptErr)
	}
}

func TestProtectedSystemDialerProtectsPacketSocketAndPreservesSource(t *testing.T) {
	server, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	_, portText, err := net.SplitHostPort(server.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := xraynet.PortFromString(portText)
	if err != nil {
		t.Fatal(err)
	}
	destination := xraynet.UDPDestination(xraynet.IPAddress(net.ParseIP("127.0.0.1").To4()), port)
	var protectedNetwork, protectedDestination string
	dialer := newProtectedSystemDialer(
		func(network, destination string, _ syscall.RawConn) error {
			protectedNetwork = network
			protectedDestination = destination
			return nil
		},
		func(string, string, uintptr, *internet.SocketConfig) error { return nil },
	)
	source := xraynet.IPAddress(net.ParseIP("127.0.0.2").To4())
	conn, err := dialer.Dial(context.Background(), source, destination, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if got := conn.LocalAddr().(*net.UDPAddr).IP; !got.Equal(net.ParseIP("127.0.0.2")) {
		t.Fatalf("local UDP source = %v, want 127.0.0.2", got)
	}
	if protectedNetwork != "udp4" || protectedDestination != destination.NetAddr() {
		t.Fatalf("protection target = (%q, %q), want (udp4, %q)", protectedNetwork, protectedDestination, destination.NetAddr())
	}
	if _, err := conn.Write([]byte("probe")); err != nil {
		t.Fatal(err)
	}
	if err := server.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 16)
	n, _, err := server.ReadFrom(buffer)
	if err != nil {
		t.Fatal(err)
	}
	if string(buffer[:n]) != "probe" {
		t.Fatalf("received packet = %q, want probe", buffer[:n])
	}
}

func TestProtectedSystemDialerUsesCurrentProtectorOnRepeatedSessions(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			_ = conn.Close()
		}
	}()

	activeGeneration := ""
	var protectedBy []string
	dialer := newProtectedSystemDialer(
		func(string, string, syscall.RawConn) error {
			protectedBy = append(protectedBy, activeGeneration)
			return nil
		},
		func(string, string, uintptr, *internet.SocketConfig) error { return nil },
	)
	destination := tcpDestination(t, listener.Addr().String())
	for _, generation := range []string{"session-1", "session-2"} {
		activeGeneration = generation
		conn, dialErr := dialer.Dial(context.Background(), nil, destination, nil)
		if dialErr != nil {
			t.Fatal(dialErr)
		}
		_ = conn.Close()
	}
	if want := []string{"session-1", "session-2"}; !reflect.DeepEqual(protectedBy, want) {
		t.Fatalf("socket protection generations = %#v, want %#v", protectedBy, want)
	}
}
