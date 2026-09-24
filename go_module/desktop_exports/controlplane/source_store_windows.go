//go:build windows

package controlplane

import (
	"fmt"
	"os/user"
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
