//go:build android || ios

package common

import (
	"sync"
	"syscall"
	log "go_client/logger"
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

	log.Infof("Start readFromUserLoop")

	for {
		select {
		case <-t.stopCh:
			return
		default:
			n, err := syscall.Read(t.tunFd, buf)
			if err != nil {
				continue
			}
			if n > 0 && t.writeFn != nil {
				_, _ = t.writeFn(buf[:n])
			}
		}
	}
}

func (t *tunTransfer) writeToUserLoop() {
	defer t.wg.Done()

	buf := make([]byte, defaultBufSize)

	log.Infof("Start writeToUserLoop")

	for {
		select {
		case <-t.stopCh:
			return
		default:
			if t.readFn == nil {
				continue
			}
        	log.Infof("[writeToUserLoop] start readFn")
			n, err := t.readFn(buf)
        	log.Infof("[writeToUserLoop] readFn, err = %v, n = %v", err, n)
			if err != nil {
				continue
			}
			if n > 0 {
				_, _ = syscall.Write(t.tunFd, buf[:n])
			}
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
