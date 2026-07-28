package vpnmanager

import (
	"fmt"
	"runtime/debug"

	"go_module/log"
)

// RecoverMessage formats a panic value for export-boundary logging.
func RecoverMessage(fnName string, recovered any) string {
	return fmt.Sprintf("panic in %s: %s", fnName, RecoveredString(recovered))
}

// RecoveredString stringifies a recovered panic value.
func RecoveredString(recovered any) string {
	switch v := recovered.(type) {
	case string:
		return v
	default:
		return fmt.Sprintf("%v", v)
	}
}

// Guard logs panics at the export boundary without mutating caller state.
func Guard(category, fnName string) func() {
	return func() {
		if recovered := recover(); recovered != nil {
			msg := RecoverMessage(fnName, recovered)
			if category != "" {
				log.Debugf(category, "%s\n%s", msg, string(debug.Stack()))
			}
		}
	}
}

// GuardErr logs panics and assigns a formatted error to errp when non-nil.
//
//nolint:gocritic // Deferred recovery must update the caller's named error result.
func GuardErr(category, fnName string, errp *error) func() {
	return func() {
		if recovered := recover(); recovered != nil {
			msg := RecoverMessage(fnName, recovered)
			if category != "" {
				log.Debugf(category, "%s\n%s", msg, string(debug.Stack()))
			}
			if errp != nil {
				*errp = fmt.Errorf("%s", msg)
			}
		}
	}
}

// GuardStatus logs panics and sets statusp to -1 when non-nil.
func GuardStatus(category, fnName string, statusp *int32) func() {
	return func() {
		if recovered := recover(); recovered != nil {
			msg := RecoverMessage(fnName, recovered)
			if category != "" {
				log.Debugf(category, "%s\n%s", msg, string(debug.Stack()))
			}
			if statusp != nil {
				*statusp = -1
			}
		}
	}
}

// GuardExport logs panics and records the message in lastErr when non-nil.
func GuardExport(category, fnName string, lastErr *LastError) func() {
	return func() {
		if recovered := recover(); recovered != nil {
			msg := RecoverMessage(fnName, recovered)
			if lastErr != nil {
				lastErr.Set(msg)
			}
			if category != "" {
				log.Debugf(category, "%s\n%s", msg, string(debug.Stack()))
			}
		}
	}
}
