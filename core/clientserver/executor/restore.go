//go:build !(android || ios)

package executor

import (
	"context"

	"core/log"
	"core/sessionapi"
)

// serviceRestorer is the part of the process binding the desktop service uses
// to bring back a session that was active before the service stopped.
type serviceRestorer interface {
	PrepareServiceRestore() func(context.Context) error
}

// prepareDesktopRestore fixes the user's intent before the control channel
// accepts commands and returns the function that starts the restore once it
// serves. The restore runs in the background: it loads the saved source and
// waits for the tunnel. A failure is logged (code only, the message can
// contain the subscription address) and never stops the service; a failed
// connection shows in the snapshot like any other failed connection.
//
// A macOS kill switch left by a backend that died stays in place while the
// restore runs. Once the restore ends, it is removed unless a network adopted
// it, so the user is neither left offline nor exposed before reconnecting.
func prepareDesktopRestore(owner serviceRestorer) (start func()) {
	restore := owner.PrepareServiceRestore()
	return func() {
		go func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					log.Errorf(desktopLogCategory, "automatic session restore panicked: %v", recovered)
				}
			}()
			if err := restore(context.Background()); err != nil {
				log.Errorf(desktopLogCategory, "automatic session restore failed: %s", sessionapi.CodeOf(err))
			}
			if err := releaseUnadoptedKillSwitch(context.Background()); err != nil {
				log.Errorf(desktopLogCategory, "leftover kill switch removal failed: %v", err)
			}
		}()
	}
}
