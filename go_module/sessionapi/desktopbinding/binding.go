// Package desktopbinding owns the one desktop session manager used by the
// gRPC server. Keeping construction here prevents transports from creating
// independent runtime owners.
package desktopbinding

import (
	"context"
	"sync"

	"go_module/sessionapi/runtimebridge"
	v2 "go_module/sessionapi/v2"
)

type platform struct{}

func (platform) PrepareTunnel(context.Context, v2.SessionRef) (v2.PlatformLease, error) {
	return lease{}, nil
}
func (platform) ProtectSocket(context.Context, v2.SessionRef, int) error { return nil }
func (platform) PublishState(context.Context, v2.StateChange)            {}

type lease struct{}

func (lease) Release(context.Context) error { return nil }

var defaultOnce sync.Once
var defaultManager *v2.Manager

func Default() *v2.Manager {
	defaultOnce.Do(func() {
		defaultManager = v2.NewManager(v2.ManagerOptions{Runtime: runtimebridge.New(nil), Platform: platform{}})
	})
	return defaultManager
}
