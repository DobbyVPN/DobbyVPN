//go:build !(android || ios)

package executor

import sessionruntime "go_module/sessionapi/runtime"

type Executor struct {
}

func recoverInterruptedState() error {
	return sessionruntime.RecoverInterruptedState()
}
