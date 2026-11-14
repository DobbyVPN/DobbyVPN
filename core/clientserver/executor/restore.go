//go:build !(android || ios)

package executor

import (
	"encoding/json"

	"core/log"
	"core/sessionapi/wire"
)

// sessionRestorer is the part of the process binding the desktop service uses
// to bring back a session that was active before the service stopped.
type sessionRestorer interface {
	Restore(onlyPreviouslyEnabled bool) string
}

// restoreDesktopSession restores the session that was active before the
// service last stopped; a session the user stopped stays stopped. It is called
// once the control channel is serving and runs in the background, because
// restoring loads the saved source and waits for the tunnel. A failure is
// logged and never stops the service; a failed connection shows in the
// snapshot like any other failed connection.
func restoreDesktopSession(owner sessionRestorer) {
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				log.Errorf(desktopLogCategory, "automatic session restore panicked: %v", recovered)
			}
		}()
		logRestoreResult(owner.Restore(true))
	}()
}

func logRestoreResult(raw string) {
	var response wire.Response[json.RawMessage]
	if err := json.Unmarshal([]byte(raw), &response); err != nil {
		log.Errorf(desktopLogCategory, "automatic session restore returned an unreadable result: %v", err)
		return
	}
	if !response.OK {
		code := ""
		if response.Error != nil {
			code = response.Error.Code
		}
		// The message can carry the subscription address, so only the code is logged.
		log.Errorf(desktopLogCategory, "automatic session restore failed: %s", code)
		return
	}
	log.Debugf(desktopLogCategory, "automatic session restore finished")
}
