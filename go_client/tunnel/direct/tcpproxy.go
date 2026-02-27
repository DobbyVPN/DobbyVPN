package direct

import (
	"fmt"
	"io"
	"math/rand"
	"net"
	"sync"
	"sync/atomic"
	"time"

	log "go_client/logger"
)

const tcpLogPrefix = "direct:tcp"

type tcpConnState int

const (
	tcpStateNew         tcpConnState = iota
	tcpStateSynReceived              // SYN received, SYN-ACK sent, waiting for ACK
	tcpStateEstablished              // connection fully open
	tcpStateFinWait                  // FIN received or sent, tearing down
	tcpStateClosed
)

// tcpProxy proxies a single TCP connection between the TUN and the real network.
type tcpProxy struct {
	key  ConnKey
	dev  *DirectIPDevice
	conn net.Conn // real OS connection

	mu    sync.Mutex
	state tcpConnState

	// ─── sequence number tracking ───
	// "their" = the client side (TUN packets)
	// "our"   = the side we present to the TUN (responses we craft)
	theirISN uint32 // initial SEQ from the SYN
	theirSeq uint32 // next expected SEQ from client

	ourISN uint32 // our initial SEQ (random)
	ourSeq uint32 // next SEQ we send in outgoing packets

	// For IP ID field
	ipID uint32

	// Window we advertise
	window uint16

	// Cached IP addresses for building response packets
	srcIP net.IP // == original DstIP (we respond as the server)
	dstIP net.IP // == original SrcIP

	srcPort uint16 // == original DstPort
	dstPort uint16 // == original SrcPort

	closedCh chan struct{}
}

func newTCPProxy(dev *DirectIPDevice, key ConnKey, srcIP, dstIP net.IP, srcPort, dstPort uint16) *tcpProxy {
	return &tcpProxy{
		key:      key,
		dev:      dev,
		state:    tcpStateNew,
		ourISN:   rand.Uint32(),
		window:   65535,
		srcIP:    append(net.IP(nil), dstIP...), // swap: we reply as the original dst
		dstIP:    append(net.IP(nil), srcIP...), // swap: we reply to original src
		srcPort:  dstPort,
		dstPort:  srcPort,
		closedCh: make(chan struct{}),
	}
}

// handlePacket processes an incoming TCP packet from the TUN.
func (p *tcpProxy) handlePacket(ipH *IPv4Header, tcpH *TCPHeader, payload []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()

	flags := tcpH.Flags

	switch p.state {
	case tcpStateNew:
		if flags&TCPFlagSYN == 0 {
			// Not a SYN — send RST
			p.sendRSTLocked(tcpH)
			p.closeLocked()
			return
		}
		p.handleSYNLocked(tcpH)

	case tcpStateSynReceived:
		if flags&TCPFlagRST != 0 {
			log.Infof("[%s] RST during handshake %v", tcpLogPrefix, p.key)
			p.closeLocked()
			return
		}
		if flags&TCPFlagACK != 0 {
			// Handshake complete
			p.state = tcpStateEstablished
			log.Infof("[%s] ESTABLISHED %v", tcpLogPrefix, p.key)
			// Start reading from real connection
			go p.readFromRemote()

			// If this ACK also carries data, process it
			if len(payload) > 0 {
				p.forwardDataLocked(tcpH, payload)
			}
		}

	case tcpStateEstablished:
		if flags&TCPFlagRST != 0 {
			log.Infof("[%s] RST in ESTABLISHED %v", tcpLogPrefix, p.key)
			p.closeLocked()
			return
		}
		if flags&TCPFlagFIN != 0 {
			p.handleFINLocked(tcpH)
			return
		}
		if len(payload) > 0 {
			p.forwardDataLocked(tcpH, payload)
		}
		// Pure ACK with no data — nothing to do
		// Update theirSeq tracking on ACKs
		if flags&TCPFlagACK != 0 && len(payload) == 0 {
			// just an ACK, no need to respond
		}

	case tcpStateFinWait:
		if flags&TCPFlagACK != 0 {
			// Final ACK received — close
			p.closeLocked()
		}

	case tcpStateClosed:
		// drop
	}
}

func (p *tcpProxy) handleSYNLocked(tcpH *TCPHeader) {
	p.theirISN = tcpH.SeqNum
	p.theirSeq = tcpH.SeqNum + 1

	p.ourSeq = p.ourISN

	// Dial the real target
	target := fmt.Sprintf("%s:%d", p.srcIP.String(), p.srcPort)
	log.Infof("[%s] SYN: dialing %s for %v", tcpLogPrefix, target, p.key)

	conn, err := net.DialTimeout("tcp", target, 10*time.Second)
	if err != nil {
		log.Infof("[%s] dial failed: %v", tcpLogPrefix, err)
		p.sendRSTLocked(tcpH)
		p.closeLocked()
		return
	}
	p.conn = conn
	p.state = tcpStateSynReceived

	// Send SYN-ACK
	// Options: MSS = 1460
	mssOpt := []byte{0x02, 0x04, 0x05, 0xB4} // MSS=1460
	p.sendTCPLocked(TCPFlagSYN|TCPFlagACK, p.ourSeq, p.theirSeq, mssOpt, nil)
	p.ourSeq++ // SYN consumes one sequence number
}

func (p *tcpProxy) forwardDataLocked(tcpH *TCPHeader, payload []byte) {
	// Update expected sequence
	p.theirSeq = tcpH.SeqNum + uint32(len(payload))

	// Forward data to real connection
	if p.conn != nil {
		go func(data []byte) {
			_, err := p.conn.Write(data)
			if err != nil {
				log.Infof("[%s] write to remote failed: %v", tcpLogPrefix, err)
			}
		}(append([]byte(nil), payload...))
	}

	// Send ACK back
	p.sendTCPLocked(TCPFlagACK, p.ourSeq, p.theirSeq, nil, nil)
}

func (p *tcpProxy) handleFINLocked(tcpH *TCPHeader) {
	p.theirSeq = tcpH.SeqNum + 1

	// Send FIN-ACK
	p.sendTCPLocked(TCPFlagFIN|TCPFlagACK, p.ourSeq, p.theirSeq, nil, nil)
	p.ourSeq++ // FIN consumes one sequence number
	p.state = tcpStateFinWait

	// Close remote connection
	if p.conn != nil {
		p.conn.Close()
	}
}

func (p *tcpProxy) sendRSTLocked(tcpH *TCPHeader) {
	ackNum := tcpH.SeqNum + 1
	if tcpH.Flags&TCPFlagSYN != 0 {
		ackNum = tcpH.SeqNum + 1
	}
	p.sendTCPLocked(TCPFlagRST|TCPFlagACK, 0, ackNum, nil, nil)
}

func (p *tcpProxy) sendTCPLocked(flags uint8, seq, ack uint32, options, payload []byte) {
	tcpH := &TCPHeader{
		SrcPort: p.srcPort,
		DstPort: p.dstPort,
		SeqNum:  seq,
		AckNum:  ack,
		Flags:   flags,
		Window:  p.window,
		Options: options,
	}

	tcpSegment := MarshalTCP(tcpH, payload)

	// Set TCP checksum
	csum := tcpChecksumFull(p.srcIP, p.dstIP, tcpSegment)
	tcpSegment[16] = byte(csum >> 8)
	tcpSegment[17] = byte(csum)

	// Build IP header
	ipH := &IPv4Header{
		ID:       uint16(atomic.AddUint32(&p.ipID, 1)),
		TTL:      64,
		Protocol: ProtoTCP,
		Flags:    0x02, // Don't Fragment
		SrcIP:    p.srcIP,
		DstIP:    p.dstIP,
	}
	ipBuf := MarshalIPv4(ipH, len(tcpSegment))

	// Combine
	pkt := make([]byte, len(ipBuf)+len(tcpSegment))
	copy(pkt, ipBuf)
	copy(pkt[len(ipBuf):], tcpSegment)

	p.dev.enqueueResponse(pkt)
}

// readFromRemote reads data from the real TCP connection and sends it back to TUN.
func (p *tcpProxy) readFromRemote() {
	buf := make([]byte, 1400) // leave room for headers within MTU
	for {
		select {
		case <-p.closedCh:
			return
		default:
		}

		if p.conn == nil {
			return
		}

		// Set read deadline to avoid blocking forever
		p.conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		n, err := p.conn.Read(buf)
		if n > 0 {
			p.mu.Lock()
			if p.state == tcpStateEstablished || p.state == tcpStateSynReceived {
				data := make([]byte, n)
				copy(data, buf[:n])
				p.sendTCPLocked(TCPFlagPSH|TCPFlagACK, p.ourSeq, p.theirSeq, nil, data)
				p.ourSeq += uint32(n)
			}
			p.mu.Unlock()
		}
		if err != nil {
			if err == io.EOF || isClosedErr(err) {
				// Remote closed — send FIN to TUN
				p.mu.Lock()
				if p.state == tcpStateEstablished {
					p.sendTCPLocked(TCPFlagFIN|TCPFlagACK, p.ourSeq, p.theirSeq, nil, nil)
					p.ourSeq++
					p.state = tcpStateFinWait
				}
				p.mu.Unlock()
			}
			return
		}
	}
}

func (p *tcpProxy) closeLocked() {
	if p.state == tcpStateClosed {
		return
	}
	p.state = tcpStateClosed
	if p.conn != nil {
		p.conn.Close()
	}

	select {
	case <-p.closedCh:
	default:
		close(p.closedCh)
	}

	// Remove from device map after a delay (let FIN/RST packets drain)
	go func() {
		time.Sleep(5 * time.Second)
		p.dev.removeTCP(p.key)
	}()
}

func isClosedErr(err error) bool {
	if err == nil {
		return false
	}
	// net.ErrClosed and similar
	return err.Error() == "use of closed network connection" ||
		err.Error() == "use of closed file"
}
