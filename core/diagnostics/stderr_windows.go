//go:build windows

package diagnostics

import (
	"os"
	"runtime/debug"

	"golang.org/x/sys/windows"
)

func redirectStderr(file *os.File) error {
	if err := windows.SetStdHandle(windows.STD_ERROR_HANDLE, windows.Handle(file.Fd())); err != nil {
		return err
	}
	// The Go runtime may have cached the startup service handle. CrashOutput
	// explicitly follows the retained file after every descriptor rotation.
	if err := debug.SetCrashOutput(file, debug.CrashOptions{}); err != nil {
		return err
	}
	return nil
}
