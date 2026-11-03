//go:build !windows

package diagnostics

import (
	"errors"
	"os"
)

func syncDirectory(path string) (resultErr error) {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, file.Close()) }()
	return file.Sync()
}

func publishFile(source, destination string) error { return os.Rename(source, destination) }

func preserveMigrationPermissions(file *os.File, path string) error {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return preservePermissions(file, info)
}
