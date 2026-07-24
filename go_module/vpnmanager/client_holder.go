package vpnmanager

import (
	"fmt"
	"sync"

	"go_module/core"
	"go_module/core/pkg"
	"go_module/log"
)

// ClientHolder tracks the active CoreClient and serializes lifecycle mutations.
type ClientHolder struct {
	mu       sync.Mutex
	client   *core.CoreClient
	category string
}

// NewClientHolder creates a mutex-protected VPN client holder.
func NewClientHolder(category string) *ClientHolder {
	return &ClientHolder{category: category}
}

func (h *ClientHolder) Lock() {
	h.mu.Lock()
}

func (h *ClientHolder) Unlock() {
	h.mu.Unlock()
}

func (h *ClientHolder) Client() *core.CoreClient {
	return h.client
}

func (h *ClientHolder) SetClient(client *core.CoreClient) {
	h.client = client
}

func (h *ClientHolder) ClearClient() {
	h.client = nil
}

// SwitchOrPrepareDevice hot-swaps the protocol device or validates a new one.
// When no client exists yet, the caller must create one with the returned device.
func (h *ClientHolder) SwitchOrPrepareDevice(config, protocol string) (pkg.ProtocolDevice, bool, error) {
	device, err := NewProtocolDevice(config, protocol, h.category)
	if err != nil {
		return nil, false, err
	}

	if h.client == nil {
		return device, false, nil
	}

	if h.category != "" {
		log.Debugf(h.category, "existing client detected, switching protocol device")
	}
	if err := h.client.SwitchDevice(device); err != nil {
		return nil, true, fmt.Errorf("switch protocol device failed: %w", err)
	}
	if h.category != "" {
		log.Debugf(h.category, "existing client protocol device switched")
	}
	return device, true, nil
}

func (h *ClientHolder) Connect() error {
	if h.client == nil {
		if h.category != "" {
			log.Debugf(h.category, "Connect failed: client is nil")
		}
		return fmt.Errorf("client is nil")
	}
	if err := h.client.Connect(); err != nil {
		if h.category != "" {
			log.Debugf(h.category, "Connect failed: %v", err)
		}
		return err
	}
	return nil
}

func (h *ClientHolder) DisconnectAndClear() error {
	if h.client == nil {
		if h.category != "" {
			log.Debugf(h.category, "Disconnect: client already nil")
		}
		return nil
	}

	err := h.client.Disconnect()
	h.client = nil
	if err != nil && h.category != "" {
		log.Debugf(h.category, "Disconnect failed: %v", err)
	}
	return err
}
