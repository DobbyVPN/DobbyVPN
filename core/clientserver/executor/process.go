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
func shutdownDesktop(closeControl func() error, serveErr error) error {
	closeErr := closeControl()
	if errors.Is(closeErr, net.ErrClosed) {
		closeErr = nil
	}
	return errors.Join(serveErr, closeErr, desktopProcessBinding().StopAndWait(context.Background()))
}

var (
	processOnce    sync.Once
	processBinding *mobilebinding.Binding
)

func desktopProcessBinding() *mobilebinding.Binding {
	processOnce.Do(func() {
		sourceStore, err := controlplane.ConnectionSourceStore()
		if err != nil {
			panic(fmt.Sprintf("failed to locate desktop connection URL storage: %v", err))
		}
		manager := sessionapi.NewManager(sessionapi.ManagerOptions{
			Runtime:     runtimebridge.New(nil),
			SourceStore: sourceStore,
		})
		processBinding = mobilebinding.NewForDesktop(manager)
	})
	return processBinding
}
