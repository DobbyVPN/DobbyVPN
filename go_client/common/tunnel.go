//go:build android || ios

package common

import (
	"encoding/binary"
	"sync"
	"syscall"

	log "go_client/logger"

	"golang.org/x/sys/unix"
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
	transferMu     sync.Mutex
	transferInst   *tunTransfer
)

func StartTransfer(fd int, readFn ReaderFunc, writeFn WriterFunc) {
	transferMu.Lock()
	defer transferMu.Unlock()

	// На всякий случай остановим старый инстанс
	if transferInst != nil {
		stopLocked()
	}

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
	go t.readFromTunLoop()
	go t.writeToTunLoop()
}

// tun -> lwip: СРЕЗАЕМ 4 байта AF
func (t *tunTransfer) readFromTunLoop() {
	defer t.wg.Done()

	buf := make([]byte, defaultBufSize)

	log.Infof("Start readFromTunLoop")

	for {
		select {
		case <-t.stopCh:
			return
		default:
			n, err := syscall.Read(t.tunFd, buf)
			if err != nil {
				continue
			}
			// utun header = 4 bytes, payload дальше
			if n <= 4 || t.writeFn == nil {
				continue
			}
			payload := buf[4:n]
			_, _ = t.writeFn(payload)
		}
	}
}

// lwip -> tun: ДОБАВЛЯЕМ 4 байта AF
func (t *tunTransfer) writeToTunLoop() {
	defer t.wg.Done()

	in := make([]byte, defaultBufSize)
	out := make([]byte, defaultBufSize+4)

	log.Infof("Start writeToTunLoop")

	for {
		select {
		case <-t.stopCh:
			return
		default:
			if t.readFn == nil {
				continue
			}

			n, err := t.readFn(in)
			if err != nil || n <= 0 {
				continue
			}

			ver := in[0] >> 4
			var af uint32
			switch ver {
			case 4:
				af = uint32(unix.AF_INET)
			case 6:
				af = uint32(unix.AF_INET6)
			default:
				continue
			}

			// utun ожидает AF в network byte order (BigEndian)
			binary.BigEndian.PutUint32(out[:4], af)
			copy(out[4:], in[:n])

			_, _ = syscall.Write(t.tunFd, out[:n+4])
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
