//go:build !windows && !android && !ios

package controlplane

import (
	"fmt"
	"os/user"
	"strconv"

	"core/sessionapi"
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

func controlSourceDirectoryOwner(home string) (*sessionapi.SourceDirectoryOwner, error) {
	uid, err := expectedPeerUID()
	if err != nil {
		return nil, err
	}
	account, err := user.LookupId(strconv.Itoa(uid))
	if err != nil {
		return nil, fmt.Errorf("resolve desktop source directory owner: %w", err)
	}
	if account.HomeDir != home {
		return nil, fmt.Errorf("desktop source directory home changed")
	}
	gid, err := strconv.Atoi(account.Gid)
	if err != nil {
		return nil, fmt.Errorf("resolve desktop source directory group: %w", err)
	}
	return &sessionapi.SourceDirectoryOwner{Root: home, UID: uid, GID: gid}, nil
}
