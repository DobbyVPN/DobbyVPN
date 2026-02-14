package tunnel

import (
	"io"
	"sync"
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
	transferInst   *tunTransfer
)

func StartTransfer(
	tun io.ReadWriteCloser,
	readFn ReaderFunc,
	writeFn WriterFunc,
) {
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
	go t.readFromTunLoop()
	go t.writeToTunLoop()
}

func (t *tunTransfer) readFromTunLoop() {
	defer t.wg.Done()

	buf := make([]byte, defaultBufSize)

	for {
		select {
		case <-t.stopCh:
			return
		default:
			n, err := t.tun.Read(buf)
			if n <= 0 || err != nil {
				continue
			}
			if t.writeFn != nil {
				_, _ = t.writeFn(buf[:n])
			}
		}
	}
}

func (t *tunTransfer) writeToTunLoop() {
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
			_, _ = t.tun.Write(buf[:n])
		}
	}
}

func StopTransfer() {
	if transferInst == nil {
		return
	}
	close(transferInst.stopCh)
	transferInst.wg.Wait()
	_ = transferInst.tun.Close()
	transferInst = nil
}
