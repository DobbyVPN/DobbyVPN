package mobilebinding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"sync"
	"time"

	"core/sessionapi"
	"core/sessionapi/runtime"
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

// NativeTunnelResult carries both the original platform failure and whether
// the callback still owns settings/resources after that failure.
type NativeTunnelResult struct {
	FD             *int32 `json:"fd,omitempty"`
	Error          string `json:"error,omitempty"`
	CleanupPending bool   `json:"cleanup_pending"`
}

func decodeTunnelResult(raw string) (NativeTunnelResult, error) {
	var result NativeTunnelResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return result, fmt.Errorf("invalid native tunnel result %q: %w", raw, err)
	}
	if result.Error != "" {
		return result, errors.New(result.Error)
	}
	return result, nil
}

func (p *platformAdapter) Acquire(_ context.Context, ref sessionapi.SessionRef) (runtime.TunnelLease, error) {
	fd, callbacks, err := p.acquire(ref)
	if err != nil {
		if callbacks != nil {
			return &tunnelLease{fd: -1, ref: ref, adapter: p, callbacks: callbacks}, err
		}
		return nil, err
	}
	fileDescriptor, err := descriptorAsUintptr(fd)
	if err != nil {
		return &tunnelLease{fd: fd, ref: ref, adapter: p, callbacks: callbacks}, err
	}
	file := os.NewFile(fileDescriptor, "mobile-tun")
	lease := &tunnelLease{file: file, fd: fd, ref: ref, adapter: p, callbacks: callbacks}
	if file == nil {
		return lease, fmt.Errorf("could not own tunnel descriptor")
	}
	return lease, nil
}

func (p *platformAdapter) acquire(ref sessionapi.SessionRef) (int32, PlatformCallbacks, error) {
	p.mu.Lock()
	callbacks := p.callbacks
	p.mu.Unlock()
	if callbacks == nil {
		return -1, nil, fmt.Errorf("platform tunnel callback is not registered")
	}
	generation, err := generationAsInt64(ref.Generation)
	if err != nil {
		return -1, nil, err
	}
	raw := callbacks.AcquireTunnel(ref.SessionID, generation)
	result, err := decodeTunnelResult(raw)
	if err != nil || result.FD == nil || *result.FD < 0 {
		if err == nil {
			err = fmt.Errorf("platform returned no tunnel descriptor: %s", raw)
		}
		// Unknown/malformed results are conservatively treated as still owned.
		if result.CleanupPending || !json.Valid([]byte(raw)) || result.FD == nil && result.Error == "" {
			return -1, callbacks, err
		}
		return -1, nil, err
	}
	fd := *result.FD
	p.mu.Lock()
	reserved := p.tunnels.reserve(fd, fdOwner{session: ref.SessionID, generation: ref.Generation})
	p.mu.Unlock()
	if !reserved {
		return -1, nil, &sessionapi.CleanupFailure{Err: fmt.Errorf("platform reused an active tunnel descriptor %d", fd)}
	}
	return fd, callbacks, nil
}

func (p *platformAdapter) release(ctx context.Context, ref sessionapi.SessionRef, fd int32, callbacks PlatformCallbacks) error {
	if callbacks == nil {
		return fmt.Errorf("platform tunnel cleanup callback is unavailable")
	}
	generation, err := generationAsInt64(ref.Generation)
	if err != nil {
		return err
	}
	ctx = sessionapi.CleanupContext(ctx)
	deadline, _ := ctx.Deadline()
	remaining := time.Until(deadline).Milliseconds()
	if remaining <= 0 {
		return fmt.Errorf("platform tunnel cleanup still pending: %w", context.DeadlineExceeded)
	}
	result, err := decodeTunnelResult(callbacks.ReleaseTunnel(ref.SessionID, generation, fd, remaining))
	if err != nil {
		return fmt.Errorf("platform tunnel cleanup failed: %w", err)
	}
	if result.CleanupPending {
		return fmt.Errorf("platform tunnel cleanup still pending")
	}
	p.mu.Lock()
	p.tunnels.release(fd, fdOwner{session: ref.SessionID, generation: ref.Generation})
	p.mu.Unlock()
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
	file      *os.File
	fd        int32
	ref       sessionapi.SessionRef
	adapter   *platformAdapter
	callbacks PlatformCallbacks
	closeOnce sync.Once
	closeErr  error
	releaseMu sync.Mutex
	released  bool
}

func (l *tunnelLease) Read(p []byte) (int, error)  { return l.file.Read(p) }
func (l *tunnelLease) Write(p []byte) (int, error) { return l.file.Write(p) }
func (l *tunnelLease) Fd() uintptr                 { return l.file.Fd() }

// Close drops only Go's duplicated descriptor after tun2socks owns its copy.
// Release keeps the platform generation active until the runtime lease ends.
func (l *tunnelLease) Close() error {
	l.closeOnce.Do(func() {
		if l.file != nil {
			l.closeErr = l.file.Close()
		}
	})
	return l.closeErr
}
func (l *tunnelLease) Release(ctx context.Context) error {
	l.releaseMu.Lock()
	defer l.releaseMu.Unlock()
	if l.released {
		return l.closeErr
	}
	closeErr := l.Close()
	releaseErr := l.adapter.release(ctx, l.ref, l.fd, l.callbacks)
	if releaseErr == nil {
		l.released = true
	}
	return errors.Join(closeErr, releaseErr)
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
