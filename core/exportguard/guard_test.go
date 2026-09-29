package exportguard

import (
	"errors"
	"testing"
)

func guardedErrorPanic() (err error) {
	defer GuardErr("", "guardedErrorPanic", &err)()
	panic("boom")
}

func TestGuardErrAssignsRecoveredError(t *testing.T) {
	err := guardedErrorPanic()
	if err == nil || err.Error() != "panic in guardedErrorPanic: boom" {
		t.Fatalf("guardedErrorPanic() = %v, want recovered panic error", err)
	}
}

func TestGuardErrLeavesNamedErrorUnchangedWithoutPanic(t *testing.T) {
	want := errors.New("existing")
	got := func() (err error) {
		defer GuardErr("", "noPanic", &err)()
		return want
	}()
	if !errors.Is(got, want) {
		t.Fatalf("guarded no-panic result = %v, want %v", got, want)
	}
}

func TestGuardErrHandlesNilErrorResult(t *testing.T) {
	func() {
		defer GuardErr("", "nilResult", nil)()
		panic("boom")
	}()
}
