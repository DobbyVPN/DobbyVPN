package tunnel

import (
	"io"
	"sync"

	"go_client/georouting"
	"go_client/tunnel/internal"
)

type ReaderFunc func(p []byte) (int, error)
type WriterFunc func(b []byte) (int, error)

type Direction int

const (
	DirOutbound Direction = iota // из ОС в сеть (читаем из TUN)
	DirInbound                   // из сети в ОС (пишем в TUN)
)

// DirectFunc — хук для обхода VPN (наш gVisor-direct).
type DirectFunc func(packet []byte, dir Direction) error

type tunTransfer struct {
	tun      io.ReadWriteCloser
	readFn   ReaderFunc
	writeFn  WriterFunc
	directFn DirectFunc
	stopCh   chan struct{}
	wg       sync.WaitGroup
}

var (
	defaultBufSize = 65536
	transferMu     sync.Mutex
	transferInst   *tunTransfer
)

// Старый интерфейс, без directFn — чтобы не ломать существующий код.
func StartTransfer(
	tun io.ReadWriteCloser,
	readFn ReaderFunc,
	writeFn WriterFunc,
) {
	directEngine := NewSimpleTCPDirect()
	StartTransferWithDirect(tun, readFn, writeFn, directEngine.Direct)
}

// Новый интерфейс: с gVisor-direct.
func StartTransferWithDirect(
	tun io.ReadWriteCloser,
	readFn ReaderFunc,
	writeFn WriterFunc,
	directFn DirectFunc,
) {
	transferMu.Lock()
	defer transferMu.Unlock()

	if transferInst != nil {
		stopLocked()
	}

	transferInst = &tunTransfer{
		tun:      tun,
		readFn:   readFn,
		writeFn:  writeFn,
		directFn: directFn,
		stopCh:   make(chan struct{}),
	}
	transferInst.startLoops()
}

func (t *tunTransfer) startLoops() {
	t.wg.Add(2)
	go t.readLoop()
	go t.writeLoop()
}

func (t *tunTransfer) readLoop() {
	defer t.wg.Done()

	raw := make([]byte, defaultBufSize)

	for {
		select {
		case <-t.stopCh:
			return
		default:
			n, err := t.tun.Read(raw)
			if err != nil || n <= 0 {
				continue
			}

			packet, ok := internal.AdaptReadPackets(raw[:n])
			if !ok {
				continue
			}

			// Геороутинг: исходящий трафик из ОС.
			action := georouting.DecideOutbound(packet)

			switch action {
			case georouting.RouteDirect:
				if t.directFn != nil {
					_ = t.directFn(packet, DirOutbound)
				}
				// Не отправляем в VPN, иначе смысл обхода теряется.
				continue

			case georouting.RouteProxy:
				if t.writeFn == nil {
					continue
				}
				_, _ = t.writeFn(packet)
			}
		}
	}
}

func (t *tunTransfer) writeLoop() {
	defer t.wg.Done()

	buf := make([]byte, defaultBufSize)

	for {
		select {
		case <-t.stopCh:
			return
		default:
			if t.readFn == nil {
				continue
			}

			n, err := t.readFn(buf)
			if err != nil || n <= 0 {
				continue
			}

			packet := buf[:n]

			// Входящий трафик из сети, который мы хотим вернуть в ОС.
			action := georouting.DecideInbound(packet)

			switch action {
			case georouting.RouteDirect:
				if t.directFn != nil {
					_ = t.directFn(packet, DirInbound)
				}
				continue

			case georouting.RouteProxy:
				encoded, ok := internal.AdaptWritePackets(packet)
				if !ok {
					continue
				}
				_, _ = t.tun.Write(encoded)
			}
		}
	}
}

func StopTransfer() {
	transferMu.Lock()
	defer transferMu.Unlock()
	stopLocked()
}

func stopLocked() {
	if transferInst == nil {
		return
	}
	close(transferInst.stopCh)
	transferInst.wg.Wait()
	transferInst = nil
}
