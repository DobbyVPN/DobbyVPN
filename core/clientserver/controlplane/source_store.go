//go:build !(android || ios)

package controlplane

import (
	"path/filepath"

	"core/sessionapi"
)

// ConnectionSourceStore keeps the accepted desktop URL in the backend's store.
func ConnectionSourceStore() (sessionapi.SourceStore, error) {
	home, err := controlUserHome()
	if err != nil {
		return nil, err
	}
	return sessionapi.FileSourceStore{
		Path: filepath.Join(home, ".dobbyvpn", "configs", "connection-url.txt"),
	}, nil
}
