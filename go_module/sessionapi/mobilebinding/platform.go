package mobilebinding

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"sync"

	"go_module/sessionapi"
	"go_module/sessionapi/runtime"
)

// SetPlatformCallbacks replaces only the platform shell callback. It does not
// replace the manager or any active session.
func (b *Binding) SetPlatformCallbacks(callbacks PlatformCallbacks) {
	if b.platform != nil {
		b.platform.setCallbacks(callbacks)
	}
}

// ProtectActiveSocket is installed into Android's protected dialer. A dial is
// accepted only while exactly one manager-prepared generation is active.
func (b *Binding) ProtectActiveSocket(fd int32) bool {
	return b.platform != nil && b.platform.protectActive(fd)
}

type platformAdapter struct {
	mu           sync.Mutex
	callbacks    PlatformCallbacks
	tunnels      tunnelFDs
	active       map[string]sessionapi.SessionRef
	stateChanges chan sessionapi.StateChange
}

func (p *platformAdapter) setCallbacks(callbacks PlatformCallbacks) {
	p.mu.Lock()
	p.callbacks = callbacks
	p.mu.Unlock()
}

func (p *platformAdapter) PrepareTunnel(_ context.Context, ref sessionapi.SessionRef) (sessionapi.PlatformLease, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if existing, ok := p.active[ref.SessionID]; ok && existing != ref {
		return nil, fmt.Errorf("another generation is still active")
	}
	p.active[ref.SessionID] = ref
	return platformLease{adapter: p, ref: ref}, nil
}

func (p *platformAdapter) Acquire(_ context.Context, ref sessionapi.SessionRef) (runtime.TunnelLease, error) {
	fd, callbacks, err := p.acquire(ref)
	if err != nil {
		return nil, err
	}
	fileDescriptor, err := descriptorAsUintptr(fd)
	if err != nil {
		_ = closeFD(fd)
		return nil, err
	}
	file := os.NewFile(fileDescriptor, "mobile-tun")
	if file == nil {
		_ = closeFD(fd)
		return nil, fmt.Errorf("could not own tunnel descriptor")
	}
	return &tunnelLease{file: file, fd: fd, ref: ref, adapter: p, callbacks: callbacks}, nil
}

func (p *platformAdapter) acquire(ref sessionapi.SessionRef) (int32, PlatformCallbacks, error) {
	p.mu.Lock()
	callbacks := p.callbacks
	p.mu.Unlock()
	if callbacks == nil {
		return 0, nil, fmt.Errorf("platform tunnel callback is not registered")
	}
	generation, err := generationAsInt64(ref.Generation)
	if err != nil {
		return 0, nil, err
	}
	fd := callbacks.AcquireTunnel(ref.SessionID, generation)
	if fd < 0 {
		return 0, nil, fmt.Errorf("platform failed to acquire a fresh tunnel")
	}
	p.mu.Lock()
	if !p.tunnels.reserve(fd, fdOwner{session: ref.SessionID, generation: ref.Generation}) {
		p.mu.Unlock()
		_ = closeFD(fd)
		releaseErr := callbacks.ReleaseTunnel(ref.SessionID, generation, fd)
		if !releaseErr {
			return 0, nil, errors.Join(fmt.Errorf("platform reused an active tunnel descriptor"), fmt.Errorf("platform tunnel cleanup failed"))
		}
		return 0, nil, fmt.Errorf("platform reused an active tunnel descriptor")
	}
	p.mu.Unlock()
	return fd, callbacks, nil
}

func (p *platformAdapter) release(ref sessionapi.SessionRef, fd int32, callbacks PlatformCallbacks) error {
	p.mu.Lock()
	p.tunnels.release(fd, fdOwner{session: ref.SessionID, generation: ref.Generation})
	p.mu.Unlock()
	if callbacks == nil {
		return fmt.Errorf("platform tunnel cleanup callback is unavailable")
	}
	generation, err := generationAsInt64(ref.Generation)
	if err != nil {
		return err
	}
	if !callbacks.ReleaseTunnel(ref.SessionID, generation, fd) {
		return fmt.Errorf("platform tunnel cleanup failed")
	}
	return nil
}

func (p *platformAdapter) ProtectSocket(_ context.Context, ref sessionapi.SessionRef, fd int) error {
	if fd < 0 {
		return fmt.Errorf("invalid socket descriptor")
	}
	p.mu.Lock()
	callbacks := p.callbacks
	p.mu.Unlock()
	if callbacks == nil {
		return fmt.Errorf("platform socket protector is not registered")
	}
	generation, err := generationAsInt64(ref.Generation)
	if err != nil {
		return err
	}
	descriptor, err := descriptorAsInt32(fd)
	if err != nil {
		return err
	}
	if !callbacks.ProtectSocket(ref.SessionID, generation, descriptor) {
		return fmt.Errorf("platform rejected socket protection")
	}
	return nil
}

func (p *platformAdapter) protectActive(fd int32) bool {
	if fd < 0 {
		return false
	}
	p.mu.Lock()
	if len(p.active) != 1 {
		p.mu.Unlock()
		return false
	}
	var ref sessionapi.SessionRef
	for _, candidate := range p.active {
		ref = candidate
	}
	callbacks := p.callbacks
	p.mu.Unlock()
	if callbacks == nil {
		return false
	}
	generation, err := generationAsInt64(ref.Generation)
	if err != nil {
		return false
	}
	return callbacks.ProtectSocket(ref.SessionID, generation, fd)
}

func (p *platformAdapter) PublishState(_ context.Context, event sessionapi.StateChange) {
	select {
	case p.stateChanges <- event:
	default:
		select {
		case <-p.stateChanges:
		default:
		}
		select {
		case p.stateChanges <- event:
		default:
		}
	}
}

type platformLease struct {
	adapter *platformAdapter
	ref     sessionapi.SessionRef
}

func (l platformLease) Release(context.Context) error {
	l.adapter.mu.Lock()
	if active, ok := l.adapter.active[l.ref.SessionID]; ok && active == l.ref {
		delete(l.adapter.active, l.ref.SessionID)
	}
	l.adapter.mu.Unlock()
	return nil
}

type tunnelLease struct {
	file        *os.File
	fd          int32
	ref         sessionapi.SessionRef
	adapter     *platformAdapter
	callbacks   PlatformCallbacks
	closeOnce   sync.Once
	closeErr    error
	releaseOnce sync.Once
}

func (l *tunnelLease) Read(p []byte) (int, error)  { return l.file.Read(p) }
func (l *tunnelLease) Write(p []byte) (int, error) { return l.file.Write(p) }
func (l *tunnelLease) Fd() uintptr                 { return l.file.Fd() }

// Close drops only Go's duplicated descriptor after tun2socks owns its copy.
// Release keeps the platform generation active until the runtime lease ends.
func (l *tunnelLease) Close() error {
	l.closeOnce.Do(func() { l.closeErr = l.file.Close() })
	return l.closeErr
}
func (l *tunnelLease) Release(context.Context) error {
	l.releaseOnce.Do(func() {
		closeErr := l.Close()
		releaseErr := l.adapter.release(l.ref, l.fd, l.callbacks)
		l.closeErr = errors.Join(closeErr, releaseErr)
	})
	return l.closeErr
}

func closeFD(fd int32) error {
	if fd < 0 {
		return nil
	}
	fileDescriptor, err := descriptorAsUintptr(fd)
	if err != nil {
		return err
	}
	return os.NewFile(fileDescriptor, "mobile-tun-close").Close()
}

func generationAsInt64(generation uint64) (int64, error) {
	if generation > math.MaxInt64 {
		return 0, fmt.Errorf("generation exceeds the native callback range")
	}
	return int64(generation), nil
}

func descriptorAsUintptr(fd int32) (uintptr, error) {
	if fd < 0 {
		return 0, fmt.Errorf("descriptor must be non-negative")
	}
	return uintptr(fd), nil
}

func descriptorAsInt32(fd int) (int32, error) {
	if fd < 0 || fd > math.MaxInt32 {
		return 0, fmt.Errorf("socket descriptor exceeds the native callback range")
	}
	return int32(fd), nil
}
