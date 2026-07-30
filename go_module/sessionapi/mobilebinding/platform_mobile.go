//go:build android || ios

package mobilebinding

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	appLog "go_module/log"
	"go_module/sessionapi/runtimebridge"
	"go_module/sessionapi/runtimecore"
	"go_module/sessionapi/v1"
)

// New creates the single authoritative session manager for a mobile process.
// The manager owns protocol/runtime lifecycle; the callback is only the narrow
// platform boundary for TUN, socket protection, and state publication.
func New(callbacks PlatformCallbacks) *Binding {
	platform := &platformAdapter{callbacks: callbacks, tunnels: newOneShotFDs(), active: make(map[string]v1.SessionRef)}
	manager := v1.NewManager(v1.ManagerOptions{Runtime: runtimebridge.New(platform), Platform: platform, Audit: appLog.SessionAuditSink{}})
	return &Binding{manager: manager, platform: platform}
}

// SetPlatformCallbacks replaces only the platform shell callback. It does not
// replace the manager or any active session.
func (b *Binding) SetPlatformCallbacks(callbacks PlatformCallbacks) {
	if b.platform != nil {
		b.platform.setCallbacks(callbacks)
	}
}

// QueueOneShotTunnel transfers an already duplicated Android descriptor to
// exactly one pending legacy session. On error the caller still owns fd and
// must close it. New APIs should use AcquireTunnel callbacks instead.
func (b *Binding) QueueOneShotTunnel(sessionID string, fd int32) error {
	if b.platform == nil {
		return fmt.Errorf("mobile platform is unavailable")
	}
	return b.platform.queue(sessionID, fd)
}

// DiscardOneShotTunnel closes a queued descriptor that can no longer belong to
// a generation (for example a failed legacy configure or a stopped session).
func (b *Binding) DiscardOneShotTunnel(sessionID string) {
	if b.platform != nil {
		b.platform.discard(sessionID)
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
	tunnels   oneShotFDs
	active    map[string]v1.SessionRef
}

func (p *platformAdapter) setCallbacks(callbacks PlatformCallbacks) {
	p.mu.Lock()
	p.callbacks = callbacks
	p.mu.Unlock()
}

func (p *platformAdapter) queue(sessionID string, fd int32) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.tunnels.queue(sessionID, fd)
}

func (p *platformAdapter) discard(sessionID string) {
	p.mu.Lock()
	fd, ok := p.tunnels.take(sessionID)
	p.mu.Unlock()
	if ok {
		_ = closeFD(fd)
	}
}

func (p *platformAdapter) PrepareTunnel(_ context.Context, ref v1.SessionRef) (v1.PlatformLease, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if existing, ok := p.active[ref.SessionID]; ok && existing != ref {
		return nil, fmt.Errorf("another generation is still active")
	}
	p.active[ref.SessionID] = ref
	return platformLease{adapter: p, ref: ref}, nil
}

func (p *platformAdapter) Acquire(_ context.Context, ref v1.SessionRef) (runtimecore.TunnelLease, error) {
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

func (p *platformAdapter) acquire(ref v1.SessionRef) (int32, PlatformCallbacks, error) {
	p.mu.Lock()
	if fd, ok := p.tunnels.take(ref.SessionID); ok {
		if !p.tunnels.reserve(fd, fdOwner{session: ref.SessionID, generation: ref.Generation}) {
			p.mu.Unlock()
			_ = closeFD(fd)
			return 0, nil, fmt.Errorf("one-shot tunnel descriptor is already active")
		}
		callbacks := p.callbacks
		p.mu.Unlock()
		return fd, callbacks, nil
	}
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

func (p *platformAdapter) release(ref v1.SessionRef, fd int32, callbacks PlatformCallbacks) {
	p.mu.Lock()
	p.tunnels.release(fd, fdOwner{session: ref.SessionID, generation: ref.Generation})
	p.mu.Unlock()
	if callbacks != nil {
		if generation, ok := generationAsInt64(ref.Generation); ok {
			callbacks.ReleaseTunnel(ref.SessionID, generation, fd)
		}
	}
}

func (p *platformAdapter) ProtectSocket(_ context.Context, ref v1.SessionRef, fd int) error {
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
	var ref v1.SessionRef
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

func (p *platformAdapter) PublishState(_ context.Context, event v1.Event) error {
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
	ref     v1.SessionRef
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
	ref         v1.SessionRef
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

// LegacyClient is a temporary bridge for the old NewVpnClient/VpnConnect API.
// It owns only command/session references; v1 remains the VPN lifecycle owner.
type LegacyClient struct {
	binding    *Binding
	mu         sync.Mutex
	session    string
	generation uint64
	command    uint64
}

func (b *Binding) NewLegacyClient() *LegacyClient { return &LegacyClient{binding: b} }

// Configure installs one legacy protocol payload as a compatibility profile.
// fd must be an already-owned duplicated Android TUN, or -1 on iOS. It is
// consumed by exactly one generation and closed on every unsuccessful path.
func (c *LegacyClient) Configure(config, protocol string, fd int32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	closeInput := func() {
		if fd >= 0 {
			_ = closeFD(fd)
		}
	}
	if c.session != "" {
		closeInput()
		return errors.New("a legacy session is already prepared; disconnect before changing protocol")
	}
	manager, ok := c.binding.manager.(*v1.Manager)
	if !ok {
		closeInput()
		return errors.New("mobile session manager is unavailable")
	}
	sessionID, err := manager.CreateSession(context.Background())
	if err != nil {
		closeInput()
		return err
	}
	cleanup := func() {
		c.binding.DiscardOneShotTunnel(sessionID)
		_ = manager.DestroySession(context.Background(), sessionID)
	}
	if fd >= 0 {
		if err := c.binding.QueueOneShotTunnel(sessionID, fd); err != nil {
			_ = manager.DestroySession(context.Background(), sessionID)
			closeInput()
			return err
		}
	}
	profile, err := legacyProfile(config, protocol)
	if err != nil {
		cleanup()
		return err
	}
	c.command++
	if _, err := manager.ConfigureCompatibilityProfile(context.Background(), sessionID, c.commandID("configure"), profile); err != nil {
		cleanup()
		return err
	}
	c.session, c.generation = sessionID, 0
	return nil
}

// Connect starts the configured profile. It intentionally does not hot-switch:
// a second call while a generation is active is a v1 conflict.
func (c *LegacyClient) Connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.session == "" {
		return errors.New("legacy session is not configured")
	}
	manager, ok := c.binding.manager.(*v1.Manager)
	if !ok {
		return errors.New("mobile session manager is unavailable")
	}
	c.command++
	result, err := manager.Start(context.Background(), c.session, c.commandID("start"), v1.StartTarget{Mode: v1.ProfileIndex, Index: 0})
	if err != nil {
		return err
	}
	c.generation = result.Generation
	return nil
}

// Disconnect asks v1 to stop the exact recorded generation, waits for its
// cleanup, and then removes the session. A stale terminal generation is safe
// to destroy and is treated as an idempotent disconnect.
func (c *LegacyClient) Disconnect() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.session == "" {
		return nil
	}
	manager, ok := c.binding.manager.(*v1.Manager)
	if !ok {
		return errors.New("mobile session manager is unavailable")
	}
	sessionID, generation := c.session, c.generation
	defer func() {
		c.binding.DiscardOneShotTunnel(sessionID)
		c.session, c.generation = "", 0
	}()
	if generation != 0 {
		c.command++
		_, err := manager.Stop(context.Background(), sessionID, c.commandID("stop"), generation)
		if err != nil && v1.CodeOf(err) != v1.FailureStaleGeneration {
			return err
		}
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		snapshot, err := manager.Snapshot(context.Background(), sessionID)
		if err != nil {
			return err
		}
		if snapshot.CleanupComplete {
			break
		}
		if time.Now().After(deadline) {
			return errors.New("legacy session cleanup did not complete")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := manager.DestroySession(context.Background(), sessionID); err != nil {
		return err
	}
	return nil
}

func (c *LegacyClient) commandID(operation string) string {
	return fmt.Sprintf("legacy-%s-%d", operation, c.command)
}

func legacyProfile(config, protocol string) (v1.RuntimeProfile, error) {
	var kind v1.Protocol
	var format v1.ConfigFormat
	switch protocol {
	case "outline":
		kind, format = v1.ProtocolOutline, v1.ConfigTransportURL
	case "xray":
		kind, format = v1.ProtocolXray, v1.ConfigJSON
	case "trusttunnel":
		kind, format = v1.ProtocolTrustTunnel, v1.ConfigTOML
	default:
		return v1.RuntimeProfile{}, errors.New("unsupported legacy protocol")
	}
	if config == "" {
		return v1.RuntimeProfile{}, errors.New("legacy protocol configuration is empty")
	}
	return v1.RuntimeProfile{
		Summary:          v1.ProfileSummary{Index: 0, Protocol: kind},
		RawTOML:          []byte(config),
		NormalizedFormat: format,
		NormalizedConfig: []byte(config),
	}, nil
}
