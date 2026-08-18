// Package desktopbinding owns the one desktop session manager and the
// compatibility protocol lifecycle. Both the legacy API exports and gRPC
// server use this package, so neither can retain an independent runtime owner.
package desktopbinding

import (
	"context"
	"sync"

	appLog "go_module/log"
	"go_module/sessionapi/runtimebridge"
	v2 "go_module/sessionapi/v2"
)

type platform struct{}

func (platform) PrepareTunnel(context.Context, v2.SessionRef) (v2.PlatformLease, error) {
	return lease{}, nil
}
func (platform) ProtectSocket(context.Context, v2.SessionRef, int) error { return nil }
func (platform) PublishState(context.Context, v2.Event) error            { return nil }

type lease struct{}

func (lease) Release(context.Context) error { return nil }

type Binding struct {
	Manager *v2.Manager
}

func New(manager *v2.Manager) *Binding {
	return &Binding{Manager: manager}
}

var defaultOnce sync.Once
var defaultBinding *Binding

func Default() *Binding {
	defaultOnce.Do(func() {
		defaultBinding = New(v2.NewManager(v2.ManagerOptions{Runtime: runtimebridge.New(nil), Platform: platform{}, Audit: appLog.SessionAuditSink{}}))
	})
	return defaultBinding
}
