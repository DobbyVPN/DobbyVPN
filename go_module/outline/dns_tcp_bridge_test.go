package outline

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

func TestDNSTCPBridgeFramesAndSerializesQueries(t *testing.T) {
	var dials atomic.Int32
	queries := make(chan []byte, 2)
	conn := newDNSTCPBridgeConn(context.Background(), "192.0.2.53:53", func(ctx context.Context, addr string) (net.Conn, error) {
		if addr != "192.0.2.53:53" {
			return nil, errors.New("unexpected DNS target")
		}
		dials.Add(1)
		client, server := net.Pipe()
		go func() {
			defer server.Close()
			var prefix [2]byte
			if _, err := io.ReadFull(server, prefix[:]); err != nil {
				return
			}
			query := make([]byte, int(binary.BigEndian.Uint16(prefix[:])))
			if _, err := io.ReadFull(server, query); err != nil {
				return
			}
			queries <- query
			response := dnsResponseForQuery(query)
			framed := make([]byte, 2+len(response))
			binary.BigEndian.PutUint16(framed[:2], uint16(len(response)))
			copy(framed[2:], response)
			_ = writeDNSFull(server, framed)
		}()
		return client, nil
	})
	defer conn.Close()

	first := dnsQuery(0x1234)
	if n, err := conn.Write(first); err != nil || n != len(first) {
		t.Fatalf("first Write() = (%d, %v), want (%d, nil)", n, err, len(first))
	}
	if got := <-queries; string(got) != string(first) {
		t.Fatalf("first framed query = %x, want %x", got, first)
	}

	second := dnsQuery(0x5678)
	secondWrite := make(chan error, 1)
	go func() {
		_, err := conn.Write(second)
		secondWrite <- err
	}()
	select {
	case err := <-secondWrite:
		t.Fatalf("second Write completed before first response was consumed: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	if got := dials.Load(); got != 1 {
		t.Fatalf("stream dials before consuming first response = %d, want 1", got)
	}

	firstResponse := dnsResponseForQuery(first)
	gotFirstResponse := make([]byte, len(firstResponse))
	if n, err := io.ReadFull(conn, gotFirstResponse); err != nil || n != len(firstResponse) {
		t.Fatalf("first ReadFull() = (%d, %v), want (%d, nil)", n, err, len(firstResponse))
	}
	if string(gotFirstResponse) != string(firstResponse) {
		t.Fatalf("first DNS response = %x, want %x", gotFirstResponse, firstResponse)
	}
	select {
	case err := <-secondWrite:
		if err != nil {
			t.Fatalf("second Write() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("second Write did not proceed after first response was consumed")
	}
	if got := <-queries; string(got) != string(second) {
		t.Fatalf("second framed query = %x, want %x", got, second)
	}
	secondResponse := dnsResponseForQuery(second)
	gotSecondResponse := make([]byte, len(secondResponse))
	if n, err := io.ReadFull(conn, gotSecondResponse); err != nil || n != len(secondResponse) {
		t.Fatalf("second ReadFull() = (%d, %v), want (%d, nil)", n, err, len(secondResponse))
	}
	if string(gotSecondResponse) != string(secondResponse) {
		t.Fatalf("second DNS response = %x, want %x", gotSecondResponse, secondResponse)
	}
}

func TestDNSTCPBridgeRejectsInvalidQueriesWithoutDial(t *testing.T) {
	var dials atomic.Int32
	conn := newDNSTCPBridgeConn(context.Background(), "192.0.2.53:53", func(context.Context, string) (net.Conn, error) {
		dials.Add(1)
		return nil, errors.New("unexpected dial")
	})
	defer conn.Close()

	valid := dnsQuery(0x1234)
	queryWithoutQuestions := append([]byte(nil), valid...)
	queryWithoutQuestions[5] = 0
	queryWithTruncatedName := append([]byte(nil), valid[:dnsHeaderLength+2]...)
	queryWithBadPointer := append([]byte(nil), valid...)
	queryWithBadPointer[dnsHeaderLength] = 0xc0
	queryWithBadPointer[dnsHeaderLength+1] = 0xff
	queryWithResponseFlag := append([]byte(nil), valid...)
	queryWithResponseFlag[2] |= 0x80
	oversized := make([]byte, maxDNSMessageLength+1)

	for name, query := range map[string][]byte{
		"short header":        valid[:dnsHeaderLength-1],
		"no questions":        queryWithoutQuestions,
		"truncated name":      queryWithTruncatedName,
		"bad compression ptr": queryWithBadPointer,
		"response flag":       queryWithResponseFlag,
		"oversized":           oversized,
	} {
		t.Run(name, func(t *testing.T) {
			if n, err := conn.Write(query); n != 0 || !errors.Is(err, errInvalidDNSQuery) {
				t.Fatalf("Write() = (%d, %v), want (0, invalid DNS query)", n, err)
			}
		})
	}
	if got := dials.Load(); got != 0 {
		t.Fatalf("stream dials = %d, want 0", got)
	}
}

func TestDNSTCPBridgeRejectsInvalidResponses(t *testing.T) {
	for name, mutate := range map[string]func([]byte){
		"wrong transaction": func(response []byte) { response[0]++ },
		"not a response":    func(response []byte) { response[2] &^= 0x80 },
	} {
		t.Run(name, func(t *testing.T) {
			query := dnsQuery(0x1234)
			response := dnsResponseForQuery(query)
			mutate(response)
			conn := newDNSTCPBridgeConn(context.Background(), "192.0.2.53:53", dnsPipeDial(response))
			defer conn.Close()
			if _, err := conn.Write(query); !errors.Is(err, errInvalidDNSResponse) {
				t.Fatalf("Write() error = %v, want invalid DNS response", err)
			}
		})
	}

	t.Run("oversized UDP response", func(t *testing.T) {
		query := dnsQuery(0x1234)
		conn := newDNSTCPBridgeConn(context.Background(), "192.0.2.53:53", func(_ context.Context, _ string) (net.Conn, error) {
			client, server := net.Pipe()
			go func() {
				defer server.Close()
				var prefix [2]byte
				if _, err := io.ReadFull(server, prefix[:]); err != nil {
					return
				}
				payload := make([]byte, int(binary.BigEndian.Uint16(prefix[:])))
				_, _ = io.ReadFull(server, payload)
				var tooLarge [2]byte
				binary.BigEndian.PutUint16(tooLarge[:], maxDNSUDPResponseLength+1)
				_ = writeDNSFull(server, tooLarge[:])
			}()
			return client, nil
		})
		defer conn.Close()
		if _, err := conn.Write(query); !errors.Is(err, errInvalidDNSResponse) {
			t.Fatalf("Write() error = %v, want invalid DNS response", err)
		}
	})
}

func TestDNSTCPBridgeErrorHasSafeRemoteAddress(t *testing.T) {
	const target = "192.0.2.53:53"
	conn := newDNSTCPBridgeConn(context.Background(), target, func(context.Context, string) (net.Conn, error) {
		return nil, errors.New("synthetic stream dial failure")
	})
	defer conn.Close()
	if remote := conn.RemoteAddr(); remote == nil || remote.String() != target {
		t.Fatalf("RemoteAddr() = %v, want a non-nil address for the DNS target", remote)
	}
	if n, err := conn.Write(dnsQuery(0x1234)); n != 0 || err == nil {
		t.Fatalf("Write() = (%d, %v), want a stream dial error", n, err)
	}
	if _, err := conn.Read(make([]byte, 64)); err == nil {
		t.Fatal("Read() after failed exchange returned no error")
	}
}

func TestDNSTCPBridgeCloseUnblocksReadAndWrite(t *testing.T) {
	t.Run("blocked reader", func(t *testing.T) {
		conn := newDNSTCPBridgeConn(context.Background(), "192.0.2.53:53", nil)
		readErr := make(chan error, 1)
		go func() {
			_, err := conn.Read(make([]byte, 16))
			readErr <- err
		}()
		if err := conn.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
		select {
		case err := <-readErr:
			if !errors.Is(err, net.ErrClosed) {
				t.Fatalf("blocked Read() error = %v, want closed", err)
			}
		case <-time.After(time.Second):
			t.Fatal("blocked Read did not exit after Close")
		}
	})

	t.Run("close cancels stream dial", func(t *testing.T) {
		started := make(chan struct{})
		conn := newDNSTCPBridgeConn(context.Background(), "192.0.2.53:53", func(ctx context.Context, _ string) (net.Conn, error) {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		})
		writeErr := make(chan error, 1)
		go func() {
			_, err := conn.Write(dnsQuery(0x1234))
			writeErr <- err
		}()
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("DNS stream dial did not start")
		}
		if err := conn.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
		select {
		case err := <-writeErr:
			if !errors.Is(err, net.ErrClosed) {
				t.Fatalf("Write() after Close error = %v, want closed", err)
			}
		case <-time.After(time.Second):
			t.Fatal("blocked Write did not exit after Close")
		}
	})

	t.Run("parent context cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		started := make(chan struct{})
		conn := newDNSTCPBridgeConn(ctx, "192.0.2.53:53", func(ctx context.Context, _ string) (net.Conn, error) {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		})
		writeErr := make(chan error, 1)
		go func() {
			_, err := conn.Write(dnsQuery(0x1234))
			writeErr <- err
		}()
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("DNS stream dial did not start")
		}
		cancel()
		select {
		case err := <-writeErr:
			if !errors.Is(err, net.ErrClosed) && !errors.Is(err, context.Canceled) {
				t.Fatalf("Write() after context cancellation error = %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("blocked Write did not exit after context cancellation")
		}
	})
}

func TestBridgeTCPDNSForRequiredPlatform(t *testing.T) {
	if got := shouldBridgeTCPDNS(); got != bridgeTCPDNSForPlatform {
		t.Fatalf("TCP DNS bridge policy = %v, platform policy = %v", got, bridgeTCPDNSForPlatform)
	}
}

func dnsQuery(id uint16) []byte {
	query := []byte{
		byte(id >> 8), byte(id), 0x01, 0x00,
		0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x07, 'e', 'x', 'a', 'm', 'p', 'l', 'e',
		0x04, 't', 'e', 's', 't', 0x00,
		0x00, 0x01, 0x00, 0x01,
	}
	return query
}

func dnsResponseForQuery(query []byte) []byte {
	response := append([]byte(nil), query...)
	response[2] = 0x81
	response[3] = 0x80
	return response
}

func dnsPipeDial(response []byte) dnsTCPDialFunc {
	return func(context.Context, string) (net.Conn, error) {
		client, server := net.Pipe()
		go func() {
			defer server.Close()
			var prefix [2]byte
			if _, err := io.ReadFull(server, prefix[:]); err != nil {
				return
			}
			query := make([]byte, int(binary.BigEndian.Uint16(prefix[:])))
			if _, err := io.ReadFull(server, query); err != nil {
				return
			}
			framed := make([]byte, 2+len(response))
			binary.BigEndian.PutUint16(framed[:2], uint16(len(response)))
			copy(framed[2:], response)
			_ = writeDNSFull(server, framed)
		}()
		return client, nil
	}
}
