//go:build !(android || ios)

package executor

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"

	"core/clientserver/controlplane"
	"core/sessionapi"
	"core/sessionapi/mobilebinding"
	"core/sessionapi/runtimebridge"
)

// Closing the listener stops new transports. The manager also fences requests
// already accepted by a handler before it waits for the generation's cleanup.
func shutdownDesktop(closeControl func() error, serveErr error, owner *mobilebinding.Binding) error {
	closeErr := closeControl()
	if errors.Is(closeErr, net.ErrClosed) {
		closeErr = nil
	}
	return errors.Join(serveErr, closeErr, owner.StopAndWait(context.Background()))
}

var (
	processOnce    sync.Once
	processBinding *mobilebinding.Binding
	// startupFailure is set before the first desktopProcessBinding call by
	// platforms whose startup recovery failed without stopping the service.
	startupFailure error
)

func desktopProcessBinding() *mobilebinding.Binding {
	processOnce.Do(func() {
		sourceStore, err := controlplane.ConnectionSourceStore()
		if err != nil {
			panic(fmt.Sprintf("failed to locate desktop connection URL storage: %v", err))
		}
		processBinding = newDesktopBinding(sourceStore, startupFailure)
	})
	return processBinding
}

func newDesktopBinding(sourceStore sessionapi.SourceStore, failure error) *mobilebinding.Binding {
	manager := sessionapi.NewManager(sessionapi.ManagerOptions{
		Runtime:        runtimebridge.New(nil),
		SourceStore:    sourceStore,
		StartupFailure: failure,
	})
	return mobilebinding.NewForDesktop(manager)
}
