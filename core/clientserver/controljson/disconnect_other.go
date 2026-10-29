//go:build !windows

package controljson

func isPlatformRoutineDisconnect(error) bool { return false }
