package tunnel

import (
	"io"
	"sync"

	"go_client/tunnel/internal"
)

type ReaderFunc func(p []byte) (int, error)
type WriterFunc func(b []byte) (int, error)

type tunTransfer struct {
	tun     io.ReadWriteCloser
	readFn  ReaderFunc
	writeFn WriterFunc
	stopCh  chan struct{}
	wg      sync.WaitGroup
}

var (
	defaultBufSize = 65536
	transferMu     sync.Mutex
	transferInst   *tunTransfer
)

func StartTransfer(
	tun io.ReadWriteCloser,
	readFn ReaderFunc,
	writeFn WriterFunc,
) {
	transferMu.Lock()
	defer transferMu.Unlock()

	if transferInst != nil {
		stopLocked()
	}

	transferInst = &tunTransfer{
		tun:     tun,
		readFn:  readFn,
		writeFn: writeFn,
		stopCh:  make(chan struct{}),
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
			if err != nil || n <= 0 || t.writeFn == nil {
				continue
			}

			packet, ok := internal.AdaptReadPackets(raw[:n])
			if !ok {
				continue
			}

			// TODO: Add georouting here

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
			if t.readFn == nil {
				continue
			}

			n, err := t.readFn(buf)
			if err != nil || n <= 0 {
				continue
			}

			packet := buf[:n]

			// TODO: Add georouting here

			encoded, ok := internal.AdaptWritePackets(packet)
			if !ok {
				continue
			}

			_, _ = t.tun.Write(encoded)
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
