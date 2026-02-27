package direct

import (
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	log "go_client/logger"
)

const udpLogPrefix = "direct:udp"

// udpIdleTimeout is how long a UDP "connection" lives without traffic.
const udpIdleTimeout = 2 * time.Minute

// udpProxy proxies a single UDP "flow" (identified by src/dst IP:port tuple).
type udpProxy struct {
	key  ConnKey
	dev  *DirectIPDevice
	conn net.Conn // real OS UDP socket

	mu     sync.Mutex
	closed bool

	// For building response packets
	srcIP   net.IP // == original DstIP
	dstIP   net.IP // == original SrcIP
	srcPort uint16 // == original DstPort
	dstPort uint16 // == original SrcPort

	ipID uint32

	timer *time.Timer
}

func newUDPProxy(dev *DirectIPDevice, key ConnKey, srcIP, dstIP net.IP, srcPort, dstPort uint16) *udpProxy {
	return &udpProxy{
		key:     key,
		dev:     dev,
		srcIP:   append(net.IP(nil), dstIP...), // swap
		dstIP:   append(net.IP(nil), srcIP...), // swap
		srcPort: dstPort,
		dstPort: srcPort,
	}
}

// start dials the real UDP target and starts reading responses.
func (u *udpProxy) start() error {
	target := fmt.Sprintf("%s:%d", u.srcIP.String(), u.srcPort)
	conn, err := net.Dial("udp", target)
	if err != nil {
		return fmt.Errorf("udp dial %s: %w", target, err)
	}
	u.conn = conn

	// Idle timer
	u.timer = time.AfterFunc(udpIdleTimeout, func() {
		log.Infof("[%s] idle timeout for %v", udpLogPrefix, u.key)
		u.Close()
	})

	// Read responses from the real network
	go u.readFromRemote()

	return nil
}

// forwardPayload sends a UDP payload to the real destination.
func (u *udpProxy) forwardPayload(payload []byte) {
	u.mu.Lock()
	defer u.mu.Unlock()

	if u.closed || u.conn == nil {
		return
	}

	// Reset idle timer
	u.timer.Reset(udpIdleTimeout)

	_, err := u.conn.Write(payload)
	if err != nil {
		log.Infof("[%s] write to remote failed for %v: %v", udpLogPrefix, u.key, err)
	}
}

// readFromRemote reads responses from the real UDP connection and sends them back to TUN.
func (u *udpProxy) readFromRemote() {
	buf := make([]byte, 65535)
	for {
		u.mu.Lock()
		if u.closed {
			u.mu.Unlock()
			return
		}
		conn := u.conn
		u.mu.Unlock()

		if conn == nil {
			return
		}

		conn.SetReadDeadline(time.Now().Add(udpIdleTimeout))
		n, err := conn.Read(buf)
		if n > 0 {
			u.mu.Lock()
			if !u.closed {
				u.timer.Reset(udpIdleTimeout)
			}
			u.mu.Unlock()
			u.sendToTUN(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

// sendToTUN constructs a UDP response packet and sends it to the TUN.
func (u *udpProxy) sendToTUN(payload []byte) {
	udpH := &UDPHeader{
		SrcPort: u.srcPort,
		DstPort: u.dstPort,
	}
	udpData := MarshalUDP(udpH, payload)

	// Set UDP checksum
	csum := udpChecksumFull(u.srcIP, u.dstIP, udpData)
	if csum == 0 {
		csum = 0xFFFF // zero checksum is transmitted as all-ones in UDP
	}
	udpData[6] = byte(csum >> 8)
	udpData[7] = byte(csum)

	// Build IP header
	ipH := &IPv4Header{
		ID:       uint16(atomic.AddUint32(&u.ipID, 1)),
		TTL:      64,
		Protocol: ProtoUDP,
		SrcIP:    u.srcIP,
		DstIP:    u.dstIP,
	}
	ipBuf := MarshalIPv4(ipH, len(udpData))

	pkt := make([]byte, len(ipBuf)+len(udpData))
	copy(pkt, ipBuf)
	copy(pkt[len(ipBuf):], udpData)

	u.dev.enqueueResponse(pkt)
}

func (u *udpProxy) Close() {
	u.mu.Lock()
	defer u.mu.Unlock()

	if u.closed {
		return
	}
	u.closed = true

	if u.timer != nil {
		u.timer.Stop()
	}
	if u.conn != nil {
		u.conn.Close()
	}

	// Remove from device map
	go func() {
		u.dev.removeUDP(u.key)
	}()
}
