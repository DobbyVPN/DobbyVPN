//go:build !windows && !android && !ios

package controlplane

import (
	"fmt"
	"os/user"
)

func controlUserHome() (string, error) {
	uid, err := expectedPeerUID()
	if err != nil {
		return "", err
	}
	account, err := user.LookupId(fmt.Sprint(uid))
	if err != nil {
		return "", fmt.Errorf("resolve desktop connection URL owner: %w", err)
	}
	if account.HomeDir == "" {
		return "", fmt.Errorf("desktop connection URL owner has no home directory")
	}
	return account.HomeDir, nil
}
