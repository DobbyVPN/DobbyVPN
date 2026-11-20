//go:build windows

package controlplane

import (
	"fmt"
	"golang.org/x/sys/windows"
	"os/user"
	"path/filepath"

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
	return filepath.Join(local, "DobbyVPN", "Configuration"), nil
}
