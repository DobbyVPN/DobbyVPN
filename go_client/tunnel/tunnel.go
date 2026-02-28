package tunnel

import (
	"errors"
	"go_client/tunnel/direct"
	"go_client/tunnel/georouting"
	"io"
	"sync"

	log "go_client/logger"
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

	// Чтобы не спамить одинаковыми логами в циклах.
	onceNoDirectOutbound sync.Once
	onceNoReadFnInbound  sync.Once
)

const logPrefix = "tunnel"

// helper для логов по горутинам
func logLoop(loop string, format string, args ...any) {
	log.Infof("["+logPrefix+":"+loop+"] "+format, args...)
}

func logLoopErr(loop string, msg string, err error) {
	log.Infof("[Error: "+logPrefix+":"+loop+"] %s: %v", msg, err)
}

// Старый интерфейс, без directFn — чтобы не ломать существующий код.
func StartTransfer(
	tun io.ReadWriteCloser,
	readFn ReaderFunc,
	writeFn WriterFunc,
) {
	directFn, err := direct.NewDirectIPDevice()
	if err != nil {
		logLoopErr("core", "failed to create DirectIPDevice", err)
		// старое поведение: просто не используем direct
		directFn = nil
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
		logLoop("core", "StartTransferWithDirect called while previous transfer is active, stopping old one")
		stopLocked()
	}

	transferInst = &tunTransfer{
		tun:      tun,
		readFn:   readFn,
		writeFn:  writeFn,
		directFn: directFn,
		stopCh:   make(chan struct{}),
	}

	logLoop("core", "starting transfer (direct=%t, readFn=%t, writeFn=%t)",
		directFn != nil, readFn != nil, writeFn != nil)

	transferInst.startLoops()
}

func (t *tunTransfer) startLoops() {
	t.wg.Add(3)
	go t.readLoop()
	go t.writeLoop()
	go t.directLoop()
}

func (t *tunTransfer) readLoop() {
	defer func() {
		logLoop("read", "stopped")
		t.wg.Done()
	}()

	logLoop("read", "started")

	raw := make([]byte, defaultBufSize)

	for {
		select {
		case <-t.stopCh:
			return
		default:
		}

		n, err := t.tun.Read(raw)
		if err != nil {
			if errors.Is(err, io.EOF) {
				// обычное завершение — можно залогировать один раз, но без спама
				logLoop("read", "tun.Read returned EOF")
				return
			}
			logLoopErr("read", "error reading from TUN", err)
			continue
		}
		if n <= 0 {
			continue
		}

		packet, ok := internal.AdaptReadPackets(raw[:n])
		if !ok {
			logLoop("read", "AdaptReadPackets returned !ok (len=%d), dropping", n)
			continue
		}

		action := georouting.DecideOutbound(packet)
		logLoop("read", "outbound packet len=%d, action=%v", len(packet), action)

		switch action {
		case georouting.RouteDirect:
			logLoop("read", "RouteDirect chosen")
			if t.directFn == nil {
				onceNoDirectOutbound.Do(func() {
					logLoop("read", "RouteDirect chosen but directFn is nil, outbound packets will fallback to drop")
				})
				continue
			}
			if _, err := t.directFn.Write(packet); err != nil {
				logLoopErr("read", "failed to write packet to directFn", err)
			}

		case georouting.RouteProxy:
			logLoop("read", "RouteProxy chosen")
			if t.writeFn == nil {
				logLoop("read", "RouteProxy chosen but writeFn is nil, dropping packet")
				continue
			}
			if _, err := t.writeFn(packet); err != nil {
				logLoopErr("read", "failed to write packet to proxy writer", err)
			}

		default:
			// на всякий случай логируем странное значение
			logLoop("read", "unknown outbound action=%v, dropping", action)
		}
	}
}

func (t *tunTransfer) writeLoop() {
	defer func() {
		logLoop("write", "stopped")
		t.wg.Done()
	}()

	logLoop("write", "started")

	buf := make([]byte, defaultBufSize)

	for {
		select {
		case <-t.stopCh:
			return
		default:
		}

		if t.readFn == nil {
			onceNoReadFnInbound.Do(func() {
				logLoop("write", "readFn is nil, inbound packets from proxy will be dropped")
			})
			continue
		}

		n, err := t.readFn(buf)
		if err != nil {
			if errors.Is(err, io.EOF) {
				logLoop("write", "readFn EOF, stopping writeLoop")
				return
			}
			logLoopErr("write", "error reading from proxy", err)
			continue
		}
		if n <= 0 {
			continue
		}

		packet := buf[:n]
		action := georouting.DecideInbound(packet)
		logLoop("write", "inbound packet len=%d, action=%v", len(packet), action)

		switch action {
		case georouting.RouteProxy:
			encoded, ok := internal.AdaptWritePackets(packet)
			if !ok {
				logLoop("write", "AdaptWritePackets returned !ok, dropping")
				continue
			}
			if _, err := t.tun.Write(encoded); err != nil {
				logLoopErr("write", "failed to write encoded packet to TUN", err)
			}

		default:
			// Например, дропаем, но логируем, чтобы понимать, почему нет трафика.
			logLoop("write", "dropping inbound packet, action=%v", action)
			continue
		}
	}
}

func (t *tunTransfer) directLoop() {
	defer func() {
		logLoop("direct", "stopped")
		t.wg.Done()
	}()

	if t.directFn == nil {
		logLoop("direct", "directFn is nil, directLoop will exit immediately")
		return
	}

	logLoop("direct", "started")

	buf := make([]byte, defaultBufSize)

	for {
		select {
		case <-t.stopCh:
			return
		default:
		}

		n, err := t.directFn.Read(buf)
		if err != nil {
			if errors.Is(err, io.EOF) {
				logLoop("direct", "directFn.Read EOF, stopping directLoop")
				return
			}
			logLoopErr("direct", "error reading from directFn", err)
			continue
		}
		if n <= 0 {
			continue
		}

		packet := buf[:n]
		logLoop("direct", "received packet from directFn len=%d", len(packet))

		encoded, ok := internal.AdaptWritePackets(packet)
		if !ok {
			logLoop("direct", "AdaptWritePackets returned !ok, dropping")
			continue
		}

		if _, err := t.tun.Write(encoded); err != nil {
			logLoopErr("direct", "failed to write encoded direct packet to TUN", err)
		}
	}
}

func StopTransfer() {
	transferMu.Lock()
	defer transferMu.Unlock()
	logLoop("core", "StopTransfer called")
	stopLocked()
}

func stopLocked() {
	if transferInst == nil {
		logLoop("core", "stopLocked: transferInst is nil, nothing to stop")
		return
	}
	logLoop("core", "stopping transfer")
	close(transferInst.stopCh)
	transferInst.wg.Wait()
	transferInst = nil
	logLoop("core", "transfer stopped")
}
