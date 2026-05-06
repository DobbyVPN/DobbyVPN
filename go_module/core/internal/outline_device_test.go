package internal

import (
	"errors"
	"net"
	"testing"
	"time"
)

func TestTruncatedDNSResponsePreservesQuestionAndSetsTC(t *testing.T) {
	query := []byte{
		0x12, 0x34, // ID
		0x01, 0x00, // recursion desired
		0x00, 0x01, // QDCOUNT
		0x00, 0x01, // ANCOUNT in malformed input; response must clear it
		0x00, 0x01, // NSCOUNT
		0x00, 0x01, // ARCOUNT
		0x07, 'e', 'x', 'a', 'm', 'p', 'l', 'e',
		0x03, 'c', 'o', 'm',
		0x00,
		0x00, 0x01, // A
		0x00, 0x01, // IN
	}

	resp, err := truncatedDNSResponse(query)
	if err != nil {
		t.Fatalf("truncatedDNSResponse returned error: %v", err)
	}

	if resp[0] != query[0] || resp[1] != query[1] {
		t.Fatalf("DNS transaction ID changed: got %x %x", resp[0], resp[1])
	}
	if resp[2]&0x80 == 0 {
		t.Fatal("DNS response bit is not set")
	}
	if resp[2]&0x02 == 0 {
		t.Fatal("DNS truncated bit is not set")
	}
	if resp[4] != 0 || resp[5] != 1 {
		t.Fatalf("QDCOUNT not preserved: got %d.%d", resp[4], resp[5])
	}
	for i := 6; i <= 11; i++ {
		if resp[i] != 0 {
			t.Fatalf("record count byte %d was not cleared: %d", i, resp[i])
		}
	}
	if string(resp[12:]) != string(query[12:]) {
		t.Fatal("DNS question payload changed")
	}
}

func TestTruncatedDNSConnDoesNotRepeatSameResponse(t *testing.T) {
	conn := newTruncatedDNSConn("1.1.1.1", 53)
	defer conn.Close()

	query := []byte{
		0xab, 0xcd,
		0x01, 0x00,
		0x00, 0x01,
		0x00, 0x00,
		0x00, 0x00,
		0x00, 0x00,
		0x00,
		0x00, 0x01,
		0x00, 0x01,
	}

	if _, err := conn.Write(query); err != nil {
		t.Fatalf("Write returned error: %v", err)
	}

	buf := make([]byte, 512)
	if n, err := conn.Read(buf); err != nil || n == 0 {
		t.Fatalf("first Read = %d, %v", n, err)
	}

	readAgain := make(chan error, 1)
	go func() {
		_, err := conn.Read(buf)
		readAgain <- err
	}()

	select {
	case err := <-readAgain:
		t.Fatalf("Read repeated without another Write: %v", err)
	case <-time.After(25 * time.Millisecond):
	}

	if err := conn.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	select {
	case err := <-readAgain:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("Read after Close error = %v, want net.ErrClosed", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Read did not unblock after Close")
	}
}
