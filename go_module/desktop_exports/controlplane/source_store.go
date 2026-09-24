//go:build !(android || ios)

package controlplane

import (
	"path/filepath"

	"go_module/sessionapi"
)

// ConnectionSourceStore keeps the accepted desktop URL in the same path used
// by the previous UI and migrates its older application path on first read.
func ConnectionSourceStore() (sessionapi.SourceStore, error) {
	home, err := controlUserHome()
	if err != nil {
		return nil, err
	}
	return sessionapi.FileSourceStore{
		Path:   filepath.Join(home, ".dobbyvpn", "configs", "connection-url.txt"),
		Legacy: filepath.Join(home, ".myapp", "configs", "connection-url.txt"),
	}, nil
}
