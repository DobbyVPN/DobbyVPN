package outline

import (
	"bytes"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

func TestTruncatedDNSConnReturnsOneResponsePerRequest(t *testing.T) {
	conn := newTruncatedDNSConn()
	query := []byte{0x12, 0x34, 0x01, 0x00, 0x00, 0x01, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00}
	if n, err := conn.Write(query); err != nil || n != len(query) {
		t.Fatalf("Write() = (%d, %v), want (%d, nil)", n, err, len(query))
	}

	response := make([]byte, len(query))
	if n, err := io.ReadFull(conn, response); err != nil || n != len(query) {
		t.Fatalf("ReadFull() = (%d, %v), want (%d, nil)", n, err, len(query))
	}
	want := append([]byte(nil), query...)
	want[2] |= 0x82
	want[6], want[7] = 0, 0
	if !bytes.Equal(response, want) {
		t.Fatalf("response = %x, want %x", response, want)
	}

	secondRead := make(chan error, 1)
	go func() {
		_, err := conn.Read(make([]byte, len(query)))
		secondRead <- err
	}()
	select {
	case err := <-secondRead:
		t.Fatalf("second Read returned without another request: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	if err := conn.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	select {
	case err := <-secondRead:
		if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
			t.Fatalf("blocked Read after Close error = %v, want EOF/closed", err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked Read did not exit after Close")
	}
}

func TestTruncatedDNSConnProducesAnotherResponseAfterAnotherRequest(t *testing.T) {
	conn := newTruncatedDNSConn()
	defer conn.Close()
	for id := byte(1); id <= 2; id++ {
		query := []byte{id, 0x00, 0x01, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
		if _, err := conn.Write(query); err != nil {
			t.Fatalf("request %d Write() error = %v", id, err)
		}
		response := make([]byte, len(query))
		if _, err := io.ReadFull(conn, response); err != nil {
			t.Fatalf("request %d ReadFull() error = %v", id, err)
		}
		if response[0] != id || response[2]&0x82 != 0x82 {
			t.Fatalf("request %d response = %x", id, response)
		}
	}
}

func TestTruncatedDNSConnCloseUnblocksWaitingWrite(t *testing.T) {
	conn := newTruncatedDNSConn()
	query := []byte{0x12, 0x34, 0x01, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	if _, err := conn.Write(query); err != nil {
		t.Fatalf("first Write() error = %v", err)
	}

	blockedWrite := make(chan error, 1)
	go func() {
		_, err := conn.Write(query)
		blockedWrite <- err
	}()
	select {
	case err := <-blockedWrite:
		t.Fatalf("second Write returned before response was consumed: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	if err := conn.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	select {
	case err := <-blockedWrite:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("blocked Write after Close error = %v, want closed", err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked Write did not exit after Close")
	}
}

func TestForceTCPDNSForCloakOrRequiredPlatform(t *testing.T) {
	if !shouldForceTCPDNS(true) {
		t.Fatal("Cloak DNS must force the TCP fallback")
	}
	if got := shouldForceTCPDNS(false); got != forceTCPDNSForPlatform {
		t.Fatalf("plain Outline force-TCP policy = %v, platform policy = %v", got, forceTCPDNSForPlatform)
	}
}
