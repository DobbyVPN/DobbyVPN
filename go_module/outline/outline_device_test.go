package outline

import (
	"net"
	"strings"
	"testing"
	"time"
)

func TestLocalProxyAliveUsesListenerAddressWithoutCredentials(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen returned error: %v", err)
	}
	defer listener.Close()

	accepted := make(chan struct{})
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			_ = conn.Close()
		}
		close(accepted)
	}()

	device := &OutlineDevice{
		listener:         listener,
		listenAddr:       listener.Addr().String(),
		proxyAddr:        "user:pass@" + listener.Addr().String(),
		websocket:        true,
		hasTCPPath:       true,
		hasUDPPath:       true,
		preferTCPDNS:     true,
		disableNonDNSUDP: true,
		startedAt:        time.Now(),
		serveState:       "running",
		serveGen:         1,
	}
	device.tcpDialAttempt.Add(2)
	device.tcpDialOK.Add(1)
	device.tcpDialErr.Add(1)
	device.udpDialAttempt.Add(3)
	device.udpDialErr.Add(1)
	device.udpDNSTruncated.Add(1)
	device.udpNonDNSReject.Add(1)

	alive, status := device.LocalProxyAlive(500 * time.Millisecond)
	if !alive {
		t.Fatalf("LocalProxyAlive returned false; status=%s", status)
	}
	if !strings.Contains(status, "localProxyAlive=true") {
		t.Fatalf("status does not report alive: %s", status)
	}
	if strings.Contains(status, "too many colons") {
		t.Fatalf("status shows credentialed proxy address was dialed: %s", status)
	}
	if !strings.Contains(status, "transport(websocket=true tcpPath=true udpPath=true preferTCPDNS=true disableNonDNSUDP=true)") {
		t.Fatalf("status does not include transport flags: %s", status)
	}
	if !strings.Contains(status, "dialStats(tcpAttempt=2 tcpOK=1 tcpErr=1 udpAttempt=3 udpOK=0 udpErr=1 udpDNSTruncated=1 udpNonDNSRejected=1 unsupported=0)") {
		t.Fatalf("status does not include dial counters: %s", status)
	}

	select {
	case <-accepted:
	case <-time.After(time.Second):
		t.Fatal("listener did not receive health probe connection")
	}
}

func TestListenAddressFromProxyAddr(t *testing.T) {
	tests := map[string]string{
		"":                          "",
		"127.0.0.1:1080":            "127.0.0.1:1080",
		"user:pass@127.0.0.1:1080":  "127.0.0.1:1080",
		"user:pass@[::1]:1080":      "[::1]:1080",
		"user@with@ats@127.0.0.1:1": "127.0.0.1:1",
	}

	for input, want := range tests {
		if got := listenAddressFromProxyAddr(input); got != want {
			t.Fatalf("listenAddressFromProxyAddr(%q) = %q, want %q", input, got, want)
		}
	}
}
