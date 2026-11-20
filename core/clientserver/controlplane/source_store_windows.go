//go:build windows

package controlplane

import (
	"fmt"
	"os/user"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

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

// WindowsInstallationSourceStore opens the same two owners as the installed
// service. An MSI custom action can inherit the installing user's profile.
func WindowsInstallationSourceStore() (sessionapi.SourceStore, error) {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Services\DobbyVPN Go backend`, registry.QUERY_VALUE)
	if err != nil {
		return nil, fmt.Errorf("open installed service registration: %w", err)
	}
	defer key.Close()
	environment, _, err := key.GetStringsValue("Environment")
	if err != nil {
		return nil, fmt.Errorf("read installed service environment: %w", err)
	}
	return windowsInstallationStore(environment, registeredProfileHome)
}

func registeredProfileHome(sid string) (string, error) {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion\ProfileList\`+sid, registry.QUERY_VALUE)
	if err != nil {
		return "", fmt.Errorf("open installed profile %s: %w", sid, err)
	}
	defer key.Close()
	home, kind, err := key.GetStringValue("ProfileImagePath")
	if err == nil && kind == registry.EXPAND_SZ {
		home, err = registry.ExpandString(home)
	}
	if err != nil {
		return "", fmt.Errorf("read installed profile %s: %w", sid, err)
	}
	if !filepath.IsAbs(home) {
		return "", fmt.Errorf("installed profile %s has a relative home", sid)
	}
	return home, nil
}

func windowsInstallationStore(environment []string, profileHome func(string) (string, error)) (sessionapi.SourceStore, error) {
	var owner string
	for _, item := range environment {
		name, value, found := strings.Cut(item, "=")
		if !found || !strings.EqualFold(name, "DOBBYVPN_CONTROL_PIPE_SID") {
			continue
		}
		sid, err := windows.StringToSid(value)
		if err != nil || owner != "" || sid.IsWellKnown(windows.WinLocalSystemSid) {
			return nil, fmt.Errorf("invalid or duplicate registered desktop SID")
		}
		owner = sid.String()
	}
	if owner == "" {
		return nil, fmt.Errorf("missing registered desktop SID")
	}
	home, err := profileHome(owner)
	if err != nil {
		return nil, err
	}
	systemHome, err := profileHome("S-1-5-18")
	if err != nil {
		return nil, err
	}
	store := sessionapi.FileSourceStore{
		Path:               filepath.Join(home, ".dobbyvpn", "configs", "connection-url.txt"),
		ProtectedDirectory: filepath.Join(systemHome, "AppData", "Local", "DobbyVPN", "Configuration"),
	}
	log.Info("controlplane", "Windows installation configuration selected for removal", map[string]any{
		"desktop_sid": owner, "source_directory": filepath.Dir(store.Path), "protected_directory": store.ProtectedDirectory,
	})
	return store, nil
}
