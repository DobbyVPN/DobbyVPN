//go:build windows

package controlplane

import (
	"fmt"
	"os/user"
	"path/filepath"

	"golang.org/x/sys/windows"

	"core/log"
	"core/sessionapi"
)

func controlUserHome() (string, error) {
	account, err := installedControlUser()
	if err != nil {
		return "", err
	}
	entry, err := user.Lookup(account)
	if err != nil {
		return "", fmt.Errorf("resolve desktop connection URL owner: %w", err)
	}
	if entry.HomeDir == "" {
		return "", fmt.Errorf("desktop connection URL owner has no home directory")
	}
	return entry.HomeDir, nil
}

func controlSourceDirectoryOwner(string) (*sessionapi.SourceDirectoryOwner, error) {
	return nil, nil
}

func protectedConfigurationDirectory() (string, error) {
	local, err := windows.KnownFolderPath(windows.FOLDERID_LocalAppData, 0)
	if err != nil {
		return "", err
	}
	directory := filepath.Join(local, "DobbyVPN", "Configuration")
	// A process under a different account resolves a different protected
	// store. Report its location without emitting any accepted configuration.
	log.Info("controlplane", "Windows protected configuration directory selected", map[string]any{"directory": directory})
	return directory, nil
}
