//go:build !(android || ios)

package executor

import sessionruntime "core/sessionapi/runtime"

type Executor struct {
}

func recoverInterruptedState() error {
	return sessionruntime.RecoverInterruptedState()
}
