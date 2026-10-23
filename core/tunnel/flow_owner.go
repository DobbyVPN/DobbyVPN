package tunnel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"

	"core/log"
)

// flowOwner retains completed routes and accounts for dials whose underlying
// proxy has not returned a socket yet. Closing seals the generation first;
// callers must close any later successful dial before ending that operation.
type flowOwner struct {
	mu          sync.Mutex
	closed      bool
	connections map[io.Closer]error
	pending     int
	settled     chan struct{}
}

func (o *flowOwner) beginDial() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return net.ErrClosed
	}
	if o.pending == 0 {
		o.settled = make(chan struct{})
	}
	o.pending++
	return nil
}

func (o *flowOwner) endDial() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.pending--
	if o.pending == 0 {
		close(o.settled)
	}
}

func (o *flowOwner) track(conn io.Closer) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return false
	}
	if o.connections == nil {
		o.connections = make(map[io.Closer]error)
	}
	o.connections[conn] = nil
	return true
}

func (o *flowOwner) release(conn io.Closer, err error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if err != nil && !errors.Is(err, net.ErrClosed) {
		if o.connections == nil {
			o.connections = make(map[io.Closer]error)
		}
		// A late dial can fail to close before ever being registered. Retain
		// that residual lease even though the generation has been sealed.
		o.connections[conn] = err
	} else {
		delete(o.connections, conn)
	}
}

func (o *flowOwner) residualErrorLocked() error {
	errs := make([]error, 0, len(o.connections))
	for _, err := range o.connections {
		if err == nil {
			err = errors.New("route connection remains open")
		}
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func (o *flowOwner) sealAndClose() {
	o.mu.Lock()
	o.closed = true
	connections := make([]io.Closer, 0, len(o.connections))
	for conn := range o.connections {
		connections = append(connections, conn)
	}
	pending := o.pending
	o.mu.Unlock()
	log.Debugf(Category, "[Router] closing route flows completed=%d pendingDials=%d", len(connections), pending)
	for _, conn := range connections {
		// Each tracked handle records its latest error and unregisters only
		// after closure succeeds. A later cleanup attempt can retry failures.
		_ = conn.Close()
	}
}

func (o *flowOwner) wait(ctx context.Context) error {
	o.mu.Lock()
	pending, settled := o.pending, o.settled
	o.mu.Unlock()
	if pending != 0 {
		select {
		case <-settled:
		case <-ctx.Done():
			o.mu.Lock()
			pending, closeErr := o.pending, o.residualErrorLocked()
			o.mu.Unlock()
			if pending != 0 {
				err := fmt.Errorf("route cleanup has %d unresolved dials: %w", pending, ctx.Err())
				err = errors.Join(closeErr, err)
				log.Errorf(Category, "[Router] route flow cleanup incomplete pendingDials=%d err=%v", pending, err)
				return err
			}
		}
	}
	o.mu.Lock()
	err := o.residualErrorLocked()
	o.mu.Unlock()
	if err != nil {
		log.Errorf(Category, "[Router] route flow cleanup failed pendingDials=0 err=%v", err)
	} else {
		log.Debugf(Category, "[Router] route flows closed pendingDials=0")
	}
	return err
}
