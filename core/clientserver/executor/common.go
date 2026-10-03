//go:build !(android || ios)

package executor

import (
	"core/diagnostics"
	"core/log"
	sessionruntime "core/sessionapi/runtime"
	"fmt"
)

type Executor struct {
}

func recoverInterruptedState() error {
	return sessionruntime.RecoverInterruptedState()
}

// Capture raw output before opening the structured sink so its own startup
// failures survive. Validate the supervisor's path before creating either file.
func initExplicitLocalLog() error {
	path, err := prepareLocalLogPath()
	if err != nil {
		return err
	}
	if err := diagnostics.CaptureStderr(path+".stderr", path); err != nil {
		return fmt.Errorf("capture backend stderr: %w", err)
	}
	return log.SetPath(path)
}
