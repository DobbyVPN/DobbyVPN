//go:build !(windows || android || ios)

package controlplane

import (
	"core/log"
	"errors"
	"net"
	"os"
	"sync"
	"time"
)

// controlSocketRebindInterval is how often a removed socket path is noticed.
const controlSocketRebindInterval = 2 * time.Second

// rebindListener re-creates the Unix listener when its socket path disappears,
// for example when a session runtime directory is cleaned up after logout.
type rebindListener struct {
	path string

	mu        sync.Mutex
	inner     net.Listener
	closed    bool
	done      chan struct{}
	watchDone chan struct{}
}

func newRebindListener(path string) (*rebindListener, error) {
	inner, err := listenControlSocket(path)
	if err != nil {
		return nil, err
	}
	listener := &rebindListener{
		path:      path,
		inner:     inner,
		done:      make(chan struct{}),
		watchDone: make(chan struct{}),
	}
	go listener.watch()
	return listener, nil
}

func (l *rebindListener) Accept() (net.Conn, error) {
	for {
		l.mu.Lock()
		inner := l.inner
		closed := l.closed
		l.mu.Unlock()
		if closed {
			return nil, net.ErrClosed
		}
		conn, err := inner.Accept()
		if err == nil {
			return conn, nil
		}
		l.mu.Lock()
		replaced := l.inner != inner
		closed = l.closed
		l.mu.Unlock()
		if replaced && !closed {
			continue
		}
		return nil, err
	}
}

func (l *rebindListener) Close() error {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil
	}
	l.closed = true
	inner := l.inner
	l.mu.Unlock()
	close(l.done)
	err := inner.Close()
	<-l.watchDone
	return err
}

func (l *rebindListener) Addr() net.Addr {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.inner.Addr()
}

func (l *rebindListener) watch() {
	defer close(l.watchDone)
	ticker := time.NewTicker(controlSocketRebindInterval)
	defer ticker.Stop()
	for {
		select {
		case <-l.done:
			return
		case <-ticker.C:
			l.rebindIfRemoved()
		}
	}
}

func (l *rebindListener) rebindIfRemoved() {
	if _, err := os.Lstat(l.path); !errors.Is(err, os.ErrNotExist) {
		return
	}
	fresh, err := listenControlSocket(l.path)
	if err != nil {
		log.Debugf("controlplane", "control socket path %s was removed; recreation failed: %v", l.path, err)
		return
	}
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		_ = fresh.Close()
		return
	}
	stale := l.inner
	l.inner = fresh
	l.mu.Unlock()
	_ = stale.Close()
	log.Debugf("controlplane", "control socket path %s was removed; listener recreated", l.path)
}
