package client

import (
	"context"
	"net"
	"testing"
	"time"

	mux "github.com/cbeuw/Cloak/internal/multiplex"
)

func TestRouteUDPReturnsWhenListenerCloses(t *testing.T) {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	created := make(chan struct{}, 1)
	go func() {
		defer close(done)
		RouteUDP(context.Background(), func() (*net.UDPConn, error) { return conn, nil }, time.Second, false, func() (*mux.Session, error) {
			created <- struct{}{}
			return nil, nil
		})
	}()

	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("RouteUDP did not return after listener close")
	}
	select {
	case <-created:
		t.Fatal("closed listener created a session")
	default:
	}
}
