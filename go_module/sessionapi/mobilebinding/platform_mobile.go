//go:build android || ios

package mobilebinding

import (
	"context"
	"fmt"
	"os"
	"sync"

	appLog "go_module/log"
	"go_module/sessionapi/runtime"
	"go_module/sessionapi/runtimebridge"
	v2 "go_module/sessionapi/v2"
)

// New creates the single authoritative session manager for a mobile process.
// The manager owns protocol/runtime lifecycle; the callback is only the narrow
// platform boundary for TUN, socket protection, and state publication.
func New(callbacks PlatformCallbacks) *Binding {
	platform := &platformAdapter{callbacks: callbacks, tunnels: newTunnelFDs(), active: make(map[string]v2.SessionRef)}
	manager := v2.NewManager(v2.ManagerOptions{Runtime: runtimebridge.New(platform), Platform: platform, Audit: appLog.SessionAuditSink{}})
	return &Binding{manager: manager, platform: platform}
}

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
	mu        sync.Mutex
	callbacks PlatformCallbacks
	tunnels   tunnelFDs
	active    map[string]v2.SessionRef
}

func (p *platformAdapter) setCallbacks(callbacks PlatformCallbacks) {
	p.mu.Lock()
	p.callbacks = callbacks
	p.mu.Unlock()
}

func (p *platformAdapter) PrepareTunnel(_ context.Context, ref v2.SessionRef) (v2.PlatformLease, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if existing, ok := p.active[ref.SessionID]; ok && existing != ref {
		return nil, fmt.Errorf("another generation is still active")
	}
	p.active[ref.SessionID] = ref
	return platformLease{adapter: p, ref: ref}, nil
}

func (p *platformAdapter) Acquire(_ context.Context, ref v2.SessionRef) (runtime.TunnelLease, error) {
	fd, callbacks, err := p.acquire(ref)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), "mobile-tun")
	if file == nil {
		_ = closeFD(fd)
		return nil, fmt.Errorf("could not own tunnel descriptor")
	}
	return &tunnelLease{file: file, fd: fd, ref: ref, adapter: p, callbacks: callbacks}, nil
}

func (p *platformAdapter) acquire(ref v2.SessionRef) (int32, PlatformCallbacks, error) {
	p.mu.Lock()
	callbacks := p.callbacks
	p.mu.Unlock()
	if callbacks == nil {
		return 0, nil, fmt.Errorf("platform tunnel callback is not registered")
	}
	generation, ok := generationAsInt64(ref.Generation)
	if !ok {
		return 0, nil, fmt.Errorf("generation exceeds mobile binding range")
	}
	fd := callbacks.AcquireTunnel(ref.SessionID, generation)
	if fd < 0 {
		return 0, nil, fmt.Errorf("platform failed to acquire a fresh tunnel")
	}
	p.mu.Lock()
	if !p.tunnels.reserve(fd, fdOwner{session: ref.SessionID, generation: ref.Generation}) {
		p.mu.Unlock()
		_ = closeFD(fd)
		return 0, nil, fmt.Errorf("platform reused an active tunnel descriptor")
	}
	p.mu.Unlock()
	return fd, callbacks, nil
}

func (p *platformAdapter) release(ref v2.SessionRef, fd int32, callbacks PlatformCallbacks) {
	p.mu.Lock()
	p.tunnels.release(fd, fdOwner{session: ref.SessionID, generation: ref.Generation})
	p.mu.Unlock()
	if callbacks != nil {
		if generation, ok := generationAsInt64(ref.Generation); ok {
			callbacks.ReleaseTunnel(ref.SessionID, generation, fd)
		}
	}
}

func (p *platformAdapter) ProtectSocket(_ context.Context, ref v2.SessionRef, fd int) error {
	if fd < 0 {
		return fmt.Errorf("invalid socket descriptor")
	}
	p.mu.Lock()
	callbacks := p.callbacks
	p.mu.Unlock()
	if callbacks == nil {
		return fmt.Errorf("platform socket protector is not registered")
	}
	generation, ok := generationAsInt64(ref.Generation)
	if !ok || !callbacks.ProtectSocket(ref.SessionID, generation, int32(fd)) {
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
	var ref v2.SessionRef
	for _, candidate := range p.active {
		ref = candidate
	}
	callbacks := p.callbacks
	p.mu.Unlock()
	if callbacks == nil {
		return false
	}
	generation, ok := generationAsInt64(ref.Generation)
	return ok && callbacks.ProtectSocket(ref.SessionID, generation, fd)
}

func (p *platformAdapter) PublishState(_ context.Context, event v2.Event) error {
	p.mu.Lock()
	callbacks := p.callbacks
	p.mu.Unlock()
	if callbacks == nil {
		return nil
	}
	generation, ok := generationAsInt64(event.Generation)
	if !ok {
		return fmt.Errorf("generation exceeds mobile binding range")
	}
	profileIndex := int32(-1)
	protocol := ""
	if event.Profile != nil {
		profileIndex = int32(event.Profile.Index)
		protocol = string(event.Profile.Protocol)
	}
	callbacks.PublishState(event.SessionID, generation, int64(event.Sequence), string(event.State), profileIndex, protocol, string(event.Failure))
	return nil
}

type platformLease struct {
	adapter *platformAdapter
	ref     v2.SessionRef
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
	ref         v2.SessionRef
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
		_ = l.Close()
		l.adapter.release(l.ref, l.fd, l.callbacks)
	})
	return l.closeErr
}

func closeFD(fd int32) error {
	if fd < 0 {
		return nil
	}
	return os.NewFile(uintptr(fd), "mobile-tun-close").Close()
}
