//go:build !(android || ios)

package executor

import (
	"core/diagnostics"
	"core/log"
	sessionruntime "core/sessionapi/runtime"
	"errors"
	"fmt"
	"runtime"
)

type Executor struct {
}

func recoverInterruptedState() error {
	return sessionruntime.RecoverInterruptedState()
}

// Capture raw output before opening the structured sink so its own startup
// failures survive. Validate the supervisor's path before creating the destination.
func initExplicitLocalLog() (resultErr error) {
	path, err := prepareLocalLogPath()
	if err != nil {
		return err
	}
	defer func() {
		if resultErr != nil {
			if err := diagnostics.CaptureFailedStartup(path); err != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("capture failed startup diagnostics: %w", err))
			}
		}
	}()
	sources := []diagnostics.LegacySource{{Path: path + ".stderr", Label: "stderr"}}
	if runtime.GOOS == "darwin" {
		sources = append(sources, diagnostics.LegacySource{Path: path + ".stdout", Label: "stdout"})
	}
	if err := diagnostics.MigrateLegacy(path, sources); err != nil {
		return fmt.Errorf("migrate backend diagnostics: %w", err)
	}
	var captureErr error
	if runtime.GOOS == "darwin" {
		captureErr = diagnostics.CaptureOutput(path, path)
	} else {
		captureErr = diagnostics.CaptureStderr(path, path)
	}
	if captureErr != nil {
		return fmt.Errorf("capture backend output: %w", captureErr)
	}
	return log.SetAppenderPath(path)
}
