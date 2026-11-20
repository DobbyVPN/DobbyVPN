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
	startupFailure     error
	retainedProtection bool
	// connectionSourceStore is a seam for the startup-path test.
	connectionSourceStore = controlplane.ConnectionSourceStore
)

func removeDesktopInstallationState(ctx context.Context) error {
	var policyErr error
	if desktopProtection != nil {
		policyErr = desktopProtection.SetEnabled(ctx, false)
	}
	store, err := connectionSourceStore()
	if err != nil {
		return errors.Join(policyErr, err)
	}
	file, ok := store.(sessionapi.FileSourceStore)
	if !ok {
		return errors.Join(policyErr, errors.New("installed configuration storage unavailable"))
	}
	return errors.Join(policyErr, file.RemoveInstallation(ctx))
}

func prepareServiceProtection() error {
	if desktopProtection == nil {
		return nil
	}
	var err error
	retainedProtection, err = desktopProtection.Recover(context.Background())
	return err
}

func desktopProcessBinding() *mobilebinding.Binding {
	processOnce.Do(func() {
		sourceStore, err := connectionSourceStore()
		if err != nil {
			panic(fmt.Sprintf("failed to locate desktop connection URL storage: %v", err))
		}
		manager := sessionapi.NewManager(sessionapi.ManagerOptions{
			Runtime:        runtimebridge.New(nil),
			SourceStore:    sourceStore,
			StartupFailure: startupFailure,
			Protection:     desktopProtection, RetainedProtection: retainedProtection,
		})
		processBinding = mobilebinding.NewForDesktop(manager)
	})
	return processBinding
}
