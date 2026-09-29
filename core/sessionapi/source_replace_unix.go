//go:build !windows

package sessionapi

import "os"

func replaceSourceFile(temporary, destination string) error {
	return os.Rename(temporary, destination)
}
