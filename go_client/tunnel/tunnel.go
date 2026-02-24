package tunnel

import (
	"go_client/tunnel/direct"
	"go_client/tunnel/georouting"
	"io"
	"sync"

	"go_client/tunnel/internal"
)

type ReaderFunc func(p []byte) (int, error)
type WriterFunc func(b []byte) (int, error)

type Direction int

const (
	DirOutbound Direction = iota // из ОС в сеть (читаем из TUN)
	DirInbound                   // из сети в ОС (пишем в TUN)
)

type tunTransfer struct {
	tun      io.ReadWriteCloser
	readFn   ReaderFunc
	writeFn  WriterFunc
	stopCh   chan struct{}
	wg       sync.WaitGroup
	directFn *direct.DirectIPDevice
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
	directFn, err := direct.NewDirectIPDevice()
	if err != nil {
		return
	}
	StartTransferWithDirect(tun, readFn, writeFn, directFn)
}

// Новый интерфейс: с gVisor-direct.
func StartTransferWithDirect(
	tun io.ReadWriteCloser,
	readFn ReaderFunc,
	writeFn WriterFunc,
	directFn *direct.DirectIPDevice,
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
	t.wg.Add(3)
	go t.readLoop()
	go t.writeLoop()
	go t.directLoop()
}

func (t *tunTransfer) readLoop() {
	defer t.wg.Done()

	raw := make([]byte, defaultBufSize)

	for {
		select {
		case <-t.stopCh:
			return
		default:
		}

		n, err := t.tun.Read(raw)
		if err != nil || n <= 0 {
			continue
		}

		packet, ok := internal.AdaptReadPackets(raw[:n])
		if !ok {
			continue
		}

		action := georouting.DecideOutbound(packet)

		switch action {
		case georouting.RouteDirect:
			if t.directFn == nil {
				continue
			}
			_, _ = t.directFn.Write(packet)

		case georouting.RouteProxy:
			if t.writeFn == nil {
				continue
			}
			_, _ = t.writeFn(packet)
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
		}

		if t.readFn == nil {
			continue
		}

		n, err := t.readFn(buf)
		if err != nil || n <= 0 {
			continue
		}

		packet := buf[:n]

		action := georouting.DecideInbound(packet)

		switch action {
		case georouting.RouteProxy:
			encoded, ok := internal.AdaptWritePackets(packet)
			if !ok {
				continue
			}
			_, _ = t.tun.Write(encoded)

		default:
			//, например, дропаем
			continue
		}
	}
}

func (t *tunTransfer) directLoop() {
	defer t.wg.Done()

	if t.directFn == nil {
		return
	}

	buf := make([]byte, defaultBufSize)

	for {
		select {
		case <-t.stopCh:
			return
		default:
		}

		n, err := t.directFn.Read(buf)
		if err != nil || n <= 0 {
			// err может быть io.EOF, тогда можно сделать паузу/continue
			continue
		}

		packet := buf[:n]

		// если хочешь — можешь тут тоже сделать DecideInboundDirect()
		// action := georouting.DecideInboundDirect(packet)

		encoded, ok := internal.AdaptWritePackets(packet)
		if !ok {
			continue
		}

		_, _ = t.tun.Write(encoded)
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
