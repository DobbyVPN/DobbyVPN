//go:build !windows

package diagnostics

import (
	"os/exec"
	"testing"

	"golang.org/x/sys/unix"
)

func checkStderrOwnership(*testing.T) {}

func writeNativeStderr(data []byte) error {
	_, err := unix.Write(2, data)
	return err
}

func runStderrChild(_ *testing.T, child *exec.Cmd) ([]byte, error) { return child.CombinedOutput() }
