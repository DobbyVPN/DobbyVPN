//go:build android || ios

package mobilebinding

import (
	"context"
	"core/sessionapi"
	"core/sessionapi/runtimebridge"
	"errors"
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
		generation, err := generationAsInt64(event.NetworkGeneration)
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
	source, ok := callbacks.(SourceCallbacks)
	if !ok {
		return nil, errors.New("native source storage is unavailable")
	}
	value := source.LoadSourceURL()
	return []byte(value), nil
}

func (p *platformAdapter) Save(ctx context.Context, value []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	callbacks := p.callbacks
	p.mu.Unlock()
	source, ok := callbacks.(SourceCallbacks)
	if !ok || !source.SaveSourceURL(string(value)) {
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
	source, ok := callbacks.(SourceCallbacks)
	if !ok || !source.ClearSourceURL() {
		return errors.New("native source storage clear failed")
	}
	return nil
}

// iOS uses the same protected native storage as its accepted source URL.
// Android attaches FileSourceStore and therefore uses Go's private file writer.
type activationCallbacks interface {
	LoadActivation() string
	SaveActivation(string) bool
}

func (p *platformAdapter) LoadActivation(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.mu.Lock()
	callbacks, ok := p.callbacks.(activationCallbacks)
	p.mu.Unlock()
	if !ok {
		return nil, errors.New("native activation storage is unavailable")
	}
	return []byte(callbacks.LoadActivation()), nil
}
func (p *platformAdapter) SaveActivation(ctx context.Context, raw []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	callbacks, ok := p.callbacks.(activationCallbacks)
	p.mu.Unlock()
	if !ok || !callbacks.SaveActivation(string(raw)) {
		return errors.New("native activation storage write failed")
	}
	return nil
}

type acceptedCallbacks interface {
	LoadAcceptedRecord() (string, error)
	SaveAcceptedRecord(string) error
	ClearAcceptedRecord() error
}

func (p *platformAdapter) acceptedStorage(ctx context.Context) (acceptedCallbacks, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	store, ok := p.callbacks.(acceptedCallbacks)
	if !ok {
		return nil, errors.New("native accepted configuration storage is unavailable")
	}
	return store, nil
}

func (p *platformAdapter) LoadAccepted(ctx context.Context) ([]byte, error) {
	store, err := p.acceptedStorage(ctx)
	if err != nil {
		return nil, err
	}
	raw, err := store.LoadAcceptedRecord()
	if err != nil || raw == "" {
		return nil, err
	}
	return []byte(raw), nil
}

func (p *platformAdapter) SaveAccepted(ctx context.Context, raw []byte) error {
	store, err := p.acceptedStorage(ctx)
	if err != nil {
		return err
	}
	return store.SaveAcceptedRecord(string(raw))
}

func (p *platformAdapter) ClearAccepted(ctx context.Context) error {
	store, err := p.acceptedStorage(ctx)
	if err != nil {
		return err
	}
	return store.ClearAcceptedRecord()
}
