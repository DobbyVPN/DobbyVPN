//go:build android || ios

package mobilebinding

import (
	"context"
	"errors"
	"go_module/sessionapi"
	"go_module/sessionapi/runtimebridge"
)

// New creates the single authoritative session manager for a mobile process.
// The manager owns protocol/runtime lifecycle; the callback is only the narrow
// platform boundary for TUN, socket protection, and state publication.
func New(callbacks PlatformCallbacks) *Binding {
	platform := &platformAdapter{
		callbacks:    callbacks,
		tunnels:      newTunnelFDs(),
		active:       make(map[string]sessionapi.SessionRef),
		stateChanges: make(chan sessionapi.StateChange, 1),
	}
	go platform.publishStateChanges()
	manager := sessionapi.NewManager(sessionapi.ManagerOptions{
		Runtime:  runtimebridge.New(platform),
		Platform: platform,
	})
	return &Binding{manager: manager, platform: platform}
}

func (p *platformAdapter) publishStateChanges() {
	for event := range p.stateChanges {
		p.mu.Lock()
		callbacks := p.callbacks
		p.mu.Unlock()
		if callbacks == nil {
			continue
		}
		generation, err := generationAsInt64(event.Generation)
		if err != nil {
			continue
		}
		callbacks.PublishState(event.SessionID, generation, string(event.State), string(event.Failure))
	}
}

func (p *platformAdapter) Load(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.mu.Lock()
	callbacks := p.callbacks
	p.mu.Unlock()
	if callbacks == nil {
		return nil, errors.New("native source storage is unavailable")
	}
	value := callbacks.LoadSourceURL()
	return []byte(value), nil
}

func (p *platformAdapter) Save(ctx context.Context, value []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	callbacks := p.callbacks
	p.mu.Unlock()
	if callbacks == nil || !callbacks.SaveSourceURL(string(value)) {
		return errors.New("native source storage write failed")
	}
	return nil
}

func (p *platformAdapter) Clear(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	callbacks := p.callbacks
	p.mu.Unlock()
	if callbacks == nil || !callbacks.ClearSourceURL() {
		return errors.New("native source storage clear failed")
	}
	return nil
}
