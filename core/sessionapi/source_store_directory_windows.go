//go:build windows

package sessionapi

import (
	"errors"
	"os"
)

func prepareSourceDirectory(dir string, owner *SourceDirectoryOwner) error {
	if owner != nil {
		return errors.New("Unix source directory ownership is unsupported on Windows")
	}
	return os.MkdirAll(dir, 0o700)
}
