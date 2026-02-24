package direct

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"

	"go_client/tunnel"
)

// tcpFlowKey — 4-tuple (srcIP, srcPort, dstIP, dstPort) для однозначной идентификации TCP-сессии.
type tcpFlowKey struct {
	srcIP   [4]byte
	dstIP   [4]byte
	srcPort uint16
	dstPort uint16
}

// SimpleTCPDirect — простая реализация Direct-трафика только для TCP.
// Она парсит IPv4+TCP, для каждого потока открывает net.Conn к dst,
// и гоняет payload туда-сюда.
type SimpleTCPDirect struct {
	mu    sync.Mutex
	conns map[tcpFlowKey]net.Conn
}

func NewSimpleTCPDirect() *SimpleTCPDirect {
	return &SimpleTCPDirect{
		conns: make(map[tcpFlowKey]net.Conn),
	}
}

// Direct реализует tunnel.DirectFunc.
func (d *SimpleTCPDirect) Direct(packet []byte, dir tunnel.Direction) error {
	switch dir {
	case tunnel.DirOutbound:
		return d.handleOutbound(packet)
	case tunnel.DirInbound:
		// В данной модели прямой трафик живёт полностью в контексте
		// net.Conn, TUN по нему не видит входящих пакетов.
		// Поэтому DirInbound просто игнорируем.
		return nil
	default:
		return nil
	}
}

func (d *SimpleTCPDirect) handleOutbound(pkt []byte) error {
	// Минимальная длина: IPv4(20) + TCP(20)
	if len(pkt) < 40 {
		return errors.New("packet too short for IPv4+TCP")
	}

	// --- IPv4 header ---
	ipHeaderLen := int(pkt[0]&0x0F) * 4
	if ipHeaderLen < 20 || len(pkt) < ipHeaderLen+20 {
		return errors.New("invalid IPv4 header length")
	}

	srcIP := [4]byte{pkt[12], pkt[13], pkt[14], pkt[15]}
	dstIP := [4]byte{pkt[16], pkt[17], pkt[18], pkt[19]}

	// --- TCP header ---
	tcpStart := ipHeaderLen
	srcPort := binary.BigEndian.Uint16(pkt[tcpStart : tcpStart+2])
	dstPort := binary.BigEndian.Uint16(pkt[tcpStart+2 : tcpStart+4])

	flags := pkt[tcpStart+13]
	syn := flags&0x02 != 0
	fin := flags&0x01 != 0
	rst := flags&0x04 != 0

	dataOffset := int((pkt[tcpStart+12] >> 4) * 4)
	if dataOffset < 20 || tcpStart+dataOffset > len(pkt) {
		return errors.New("invalid TCP data offset")
	}
	payload := pkt[tcpStart+dataOffset:]

	key := tcpFlowKey{
		srcIP:   srcIP,
		dstIP:   dstIP,
		srcPort: srcPort,
		dstPort: dstPort,
	}

	// RST — сразу закрываем, если есть.
	if rst {
		d.closeConn(key)
		return nil
	}

	// Новый поток: SYN без существующего коннекта.
	if syn && d.getConn(key) == nil {
		addr := net.IP(dstIP[:]).String()
		target := net.JoinHostPort(addr, itoaPort(dstPort))

		conn, err := net.Dial("tcp", target)
		if err != nil {
			// не смогли открыть direct-соединение — поток фактически умер
			return err
		}

		d.setConn(key, conn)

		// Запускаем горутину чтения из conn (ответы с сервера мы тут не
		// переводим обратно в IP-пакеты, а просто «съедаем»),
		// т.к. для Direct-режима нас интересует только то, что
		// трафик ушёл мимо VPN.
		go func() {
			defer d.closeConn(key)
			buf := make([]byte, 64*1024)
			for {
				_, err := conn.Read(buf)
				if err != nil {
					return
				}
			}
		}()
	}

	conn := d.getConn(key)
	if conn == nil {
		// Нет коннекта — ничего не делаем.
		return nil
	}

	// FIN — после отправки payload можно закрывать сессию.
	if fin {
		defer d.closeConn(key)
	}

	// Пишем только payload (данные TCP, без заголовков).
	if len(payload) > 0 {
		_, err := conn.Write(payload)
		if err != nil && !errors.Is(err, io.EOF) {
			d.closeConn(key)
			return err
		}
	}

	return nil
}

// --- вспомогательные методы ---

func (d *SimpleTCPDirect) getConn(key tcpFlowKey) net.Conn {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.conns[key]
}

func (d *SimpleTCPDirect) setConn(key tcpFlowKey, conn net.Conn) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.conns[key] = conn
}

func (d *SimpleTCPDirect) closeConn(key tcpFlowKey) {
	d.mu.Lock()
	conn, ok := d.conns[key]
	if ok {
		delete(d.conns, key)
	}
	d.mu.Unlock()

	if conn != nil {
		_ = conn.Close()
	}
}

func itoaPort(p uint16) string {
	// чтобы не тянуть strconv по одной функции
	// (но можешь заменить на strconv.Itoa(int(p)) если хочешь).
	n := int(p)
	if n == 0 {
		return "0"
	}
	buf := [5]byte{}
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
