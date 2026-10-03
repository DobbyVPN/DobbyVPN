//go:build !windows

package diagnostics

import (
	"os"

	"golang.org/x/sys/unix"
)

func redirectStderr(file *os.File) error { return unix.Dup2(int(file.Fd()), 2) }
