//go:build windows

package controlplane

import (
	"fmt"
	"os"
	"os/user"
	"strings"
)

func installedControlUser() (string, error) {
	account := strings.TrimSpace(os.Getenv("DOBBYVPN_CONTROL_PIPE_USER"))
	if account == "" {
		current, err := user.Current()
		if err != nil {
			return "", fmt.Errorf("resolve installed desktop user: %w", err)
		}
		account = current.Username
	}
	upper := strings.ToUpper(account)
	if account == "" || upper == "SYSTEM" || upper == `NT AUTHORITY\SYSTEM` {
		return "", fmt.Errorf("Windows installed-user identity is unavailable for desktop control")
	}
	return account, nil
}
