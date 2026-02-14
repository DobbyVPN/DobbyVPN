package tunnel

import (
	"go_client/tunnel/internal"
	"sync"
)

type ReaderFunc func(p []byte) (int, error)
type WriterFunc func(b []byte) (int, error)

type tunTransfer struct {
	tunFd   int
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
	fd int,
	readFn ReaderFunc,
	writeFn WriterFunc,
) {
	transferInst = &tunTransfer{
		tunFd:   fd,
		readFn:  readFn,
		writeFn: writeFn,
		stopCh:  make(chan struct{}),
	}
	transferInst.startLoops()
}

func (t *tunTransfer) startLoops() {
	t.wg.Add(2)
	go t.readFromUserLoop()
	go t.writeToUserLoop()
}

func (t *tunTransfer) readFromUserLoop() {
	defer t.wg.Done()

	buf := make([]byte, defaultBufSize)

	for {
		select {
		case <-t.stopCh:
			return
		default:
			n, err := internal.Read(t.tunFd, buf)
			if n <= 0 || err != nil {
				continue
			}
			if t.writeFn != nil {
				_, _ = t.writeFn(buf[:n])
			}
		}
	}
}

func (t *tunTransfer) writeToUserLoop() {
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
			internal.Write(t.tunFd, n, buf)
		}
	}
}

func StopTransfer() {
	if transferInst == nil {
		return
	}
	close(transferInst.stopCh)
	transferInst.wg.Wait()
	transferInst = nil
}
