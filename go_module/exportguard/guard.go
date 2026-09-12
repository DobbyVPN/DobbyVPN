// Package exportguard contains the minimal panic boundaries shared by native
// export packages. It owns no VPN or protocol lifecycle state.
package exportguard

import (
	"fmt"
	"runtime/debug"

	"go_module/log"
)

type errorResultPointer interface {
	*error
}

type errorResultSetter[T errorResultPointer] struct {
	target T
}

func (setter errorResultSetter[T]) set(value error) {
	if setter.target != nil {
		*setter.target = value
	}
}

func recoveredMessage(fnName string, recovered any) string {
	switch value := recovered.(type) {
	case string:
		return fmt.Sprintf("panic in %s: %s", fnName, value)
	default:
		return fmt.Sprintf("panic in %s: %v", fnName, value)
	}
}

func report(category, fnName string, recovered any) string {
	message := recoveredMessage(fnName, recovered)
	if category != "" {
		log.Debugf(category, "%s\n%s", message, string(debug.Stack()))
	}
	return message
}

// Guard catches a panic at an export boundary without mutating caller state.
func Guard(category, fnName string) func() {
	return func() {
		if recovered := recover(); recovered != nil {
			_ = report(category, fnName, recovered)
		}
	}
}

// GuardErr catches a panic and assigns a safe error to the named result.
func GuardErr[T errorResultPointer](category, fnName string, errp T) func() {
	setter := errorResultSetter[T]{target: errp}
	return func() {
		if recovered := recover(); recovered != nil {
			message := report(category, fnName, recovered)
			setter.set(fmt.Errorf("%s", message))
		}
	}
}

// GuardStatus catches a panic and marks an integer export as failed.
func GuardStatus(category, fnName string, statusp *int32) func() {
	return func() {
		if recovered := recover(); recovered != nil {
			_ = report(category, fnName, recovered)
			if statusp != nil {
				*statusp = -1
			}
		}
	}
}
