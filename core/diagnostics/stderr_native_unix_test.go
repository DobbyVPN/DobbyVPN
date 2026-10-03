//go:build !windows

package diagnostics

import (
	"golang.org/x/sys/unix"
	"os/exec"
	"testing"
)

func checkStderrOwnership(*testing.T) {}

func writeNativeStderr(data []byte) error {
	_, err := unix.Write(2, data)
	return err
}

func runStderrChild(_ *testing.T, child *exec.Cmd) ([]byte, error) { return child.CombinedOutput() }
