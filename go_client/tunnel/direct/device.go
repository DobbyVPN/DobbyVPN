package direct

import (
	"fmt"
	"net"
	"sync"

	log "go_client/logger"
)

const deviceLogPrefix = "direct:device"

// ConnKey uniquely identifies a connection (5-tuple without protocol,
// because TCP and UDP use separate maps).
type ConnKey struct {
	SrcIP   [4]byte
	DstIP   [4]byte
	SrcPort uint16
	DstPort uint16
}

func makeConnKey(srcIP, dstIP net.IP, srcPort, dstPort uint16) ConnKey {
	var k ConnKey
	copy(k.SrcIP[:], srcIP.To4())
	copy(k.DstIP[:], dstIP.To4())
	k.SrcPort = srcPort
	k.DstPort = dstPort
	return k
}

// DirectIPDevice accepts raw IP packets, parses them, and proxies
// TCP/UDP connections directly to the internet through the real OS network stack.
// Response packets are reconstructed and returned via Read().
// No gVisor or external userspace network stack is required.
type DirectIPDevice struct {
	responses chan []byte // buffered channel for response packets
	tcpConns  sync.Map    // ConnKey -> *tcpProxy
	udpConns  sync.Map    // ConnKey -> *udpProxy

	closeOnce sync.Once
	closedCh  chan struct{}
}

func NewDirectIPDevice() (*DirectIPDevice, error) {
	d := &DirectIPDevice{
		responses: make(chan []byte, 4096),
		closedCh:  make(chan struct{}),
	}
	log.Infof("[%s] created (pure-Go transparent proxy)", deviceLogPrefix)
	return d, nil
}

// Write accepts a raw IP packet from the TUN and dispatches it
// to the appropriate TCP or UDP proxy.
func (d *DirectIPDevice) Write(pkt []byte) (int, error) {
	select {
	case <-d.closedCh:
		return 0, fmt.Errorf("device closed")
	default:
	}

	if len(pkt) == 0 {
		return 0, nil
	}

	// Only IPv4 for now
	if pkt[0]>>4 != 4 {
		return len(pkt), nil // silently drop non-IPv4
	}

	ipH, transportData, err := ParseIPv4(pkt)
	if err != nil {
		return 0, fmt.Errorf("parse IPv4: %w", err)
	}

	switch ipH.Protocol {
	case ProtoTCP:
		d.handleTCP(ipH, transportData)
	case ProtoUDP:
		d.handleUDP(ipH, transportData)
	default:
		// Silently drop ICMP, etc. for now
	}

	return len(pkt), nil
}

// Read returns the next response packet to be written back to the TUN.
// Blocks until a packet is available or the device is closed.
func (d *DirectIPDevice) Read(buf []byte) (int, error) {
	select {
	case pkt := <-d.responses:
		n := copy(buf, pkt)
		return n, nil
	case <-d.closedCh:
		return 0, fmt.Errorf("device closed")
	}
}

func (d *DirectIPDevice) Close() error {
	d.closeOnce.Do(func() {
		log.Infof("[%s] closing", deviceLogPrefix)
		close(d.closedCh)

		// Close all TCP connections
		d.tcpConns.Range(func(key, value any) bool {
			if tp, ok := value.(*tcpProxy); ok {
				tp.mu.Lock()
				tp.closeLocked()
				tp.mu.Unlock()
			}
			d.tcpConns.Delete(key)
			return true
		})

		// Close all UDP connections
		d.udpConns.Range(func(key, value any) bool {
			if up, ok := value.(*udpProxy); ok {
				up.Close()
			}
			d.udpConns.Delete(key)
			return true
		})
	})
	return nil
}

// ─── internal ───

func (d *DirectIPDevice) handleTCP(ipH *IPv4Header, data []byte) {
	tcpH, payload, err := ParseTCP(data)
	if err != nil {
		log.Infof("[%s] parse TCP failed: %v", deviceLogPrefix, err)
		return
	}

	key := makeConnKey(ipH.SrcIP, ipH.DstIP, tcpH.SrcPort, tcpH.DstPort)

	// Try to find existing connection
	if val, ok := d.tcpConns.Load(key); ok {
		tp := val.(*tcpProxy)
		tp.handlePacket(ipH, tcpH, payload)
		return
	}

	// New connection — must be a SYN
	if tcpH.Flags&TCPFlagSYN == 0 {
		// Not a SYN for an unknown connection — send RST
		log.Infof("[%s] non-SYN for unknown connection, sending RST", deviceLogPrefix)
		d.sendRSTForUnknown(ipH, tcpH)
		return
	}

	tp := newTCPProxy(d, key, ipH.SrcIP, ipH.DstIP, tcpH.SrcPort, tcpH.DstPort)
	d.tcpConns.Store(key, tp)
	tp.handlePacket(ipH, tcpH, payload)
}

func (d *DirectIPDevice) handleUDP(ipH *IPv4Header, data []byte) {
	udpH, payload, err := ParseUDP(data)
	if err != nil {
		log.Infof("[%s] parse UDP failed: %v", deviceLogPrefix, err)
		return
	}

	key := makeConnKey(ipH.SrcIP, ipH.DstIP, udpH.SrcPort, udpH.DstPort)

	// Try to find existing flow
	if val, ok := d.udpConns.Load(key); ok {
		up := val.(*udpProxy)
		up.forwardPayload(payload)
		return
	}

	// New UDP flow
	up := newUDPProxy(d, key, ipH.SrcIP, ipH.DstIP, udpH.SrcPort, udpH.DstPort)
	if err := up.start(); err != nil {
		log.Infof("[%s] udp proxy start failed: %v", deviceLogPrefix, err)
		return
	}
	d.udpConns.Store(key, up)
	up.forwardPayload(payload)
}

func (d *DirectIPDevice) enqueueResponse(pkt []byte) {
	select {
	case d.responses <- pkt:
	case <-d.closedCh:
	default:
		// Response channel full — drop packet (backpressure)
		log.Infof("[%s] response channel full, dropping packet", deviceLogPrefix)
	}
}

func (d *DirectIPDevice) removeTCP(key ConnKey) {
	d.tcpConns.Delete(key)
}

func (d *DirectIPDevice) removeUDP(key ConnKey) {
	d.udpConns.Delete(key)
}

// sendRSTForUnknown sends a RST for a TCP packet to an unknown connection.
func (d *DirectIPDevice) sendRSTForUnknown(ipH *IPv4Header, tcpH *TCPHeader) {
	rstH := &TCPHeader{
		SrcPort: tcpH.DstPort,
		DstPort: tcpH.SrcPort,
		SeqNum:  tcpH.AckNum,
		AckNum:  tcpH.SeqNum + 1,
		Flags:   TCPFlagRST | TCPFlagACK,
		Window:  0,
	}

	tcpSegment := MarshalTCP(rstH, nil)
	csum := tcpChecksumFull(ipH.DstIP, ipH.SrcIP, tcpSegment)
	tcpSegment[16] = byte(csum >> 8)
	tcpSegment[17] = byte(csum)

	rstIP := &IPv4Header{
		ID:       0,
		TTL:      64,
		Protocol: ProtoTCP,
		Flags:    0x02,
		SrcIP:    ipH.DstIP,
		DstIP:    ipH.SrcIP,
	}
	ipBuf := MarshalIPv4(rstIP, len(tcpSegment))

	pkt := make([]byte, len(ipBuf)+len(tcpSegment))
	copy(pkt, ipBuf)
	copy(pkt[len(ipBuf):], tcpSegment)

	d.enqueueResponse(pkt)
}
