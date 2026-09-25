//go:build windows

package controlplane

import (
	"fmt"
	"os"
	"os/user"
	"strings"

	"golang.org/x/sys/windows"
)

func installedControlUser() (string, error) {
	account := strings.TrimSpace(os.Getenv("DOBBYVPN_CONTROL_PIPE_USER"))
	if account == "" {
		configuredSID := strings.TrimSpace(os.Getenv("DOBBYVPN_CONTROL_PIPE_SID"))
		if configuredSID != "" {
			sid, err := windows.StringToSid(configuredSID)
			if err != nil {
				return "", fmt.Errorf("parse installed-user SID for desktop control: %w", err)
			}
			name, domain, _, err := sid.LookupAccount("")
			if err != nil {
				return "", fmt.Errorf("resolve installed-user account for desktop control: %w", err)
			}
			account = name
			if domain != "" {
				account = domain + `\` + name
			}
		} else {
			current, err := user.Current()
			if err != nil {
				return "", fmt.Errorf("resolve installed desktop user: %w", err)
			}
			account = current.Username
		}
	}
	upper := strings.ToUpper(account)
	if account == "" || upper == "SYSTEM" || upper == `NT AUTHORITY\SYSTEM` {
		return "", fmt.Errorf("Windows installed-user identity is unavailable for desktop control")
	}
	return account, nil
}

func installedControlUserSID() (*windows.SID, error) {
	var installedSID *windows.SID
	configuredSID := strings.TrimSpace(os.Getenv("DOBBYVPN_CONTROL_PIPE_SID"))
	if configuredSID != "" {
		parsed, err := windows.StringToSid(configuredSID)
		if err != nil {
			return nil, fmt.Errorf("parse installed-user SID for desktop control: %w", err)
		}
		installedSID = parsed
	} else {
		account, err := installedControlUser()
		if err != nil {
			return nil, err
		}
		resolved, _, _, err := windows.LookupSID("", account)
		if err != nil {
			return nil, fmt.Errorf("resolve installed-user SID for desktop control: %w", err)
		}
		installedSID = resolved
	}

	systemSID, err := windows.StringToSid("S-1-5-18")
	if err != nil {
		return nil, err
	}
	if installedSID.Equals(systemSID) {
		return nil, fmt.Errorf("Windows installed-user identity cannot be SYSTEM")
	}
	return installedSID, nil
}
