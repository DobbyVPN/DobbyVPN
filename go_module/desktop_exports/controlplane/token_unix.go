//go:build !(windows || android || ios)

package controlplane

import (
	"fmt"
	"os"
)

func secureControlTokenFile(path string) error { return os.Chmod(path, 0600) }

func platformControlTokenPath() (string, error) { return tokenPathInUserConfig() }
func verifyControlTokenPermissions(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Mode().Perm() != 0600 {
		return fmt.Errorf("desktop control token permissions must be 0600")
	}
	return nil
}
