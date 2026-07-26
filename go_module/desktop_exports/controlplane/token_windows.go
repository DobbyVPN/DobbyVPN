//go:build windows

package controlplane

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

func platformControlTokenPath() (string, error) {
	programData := os.Getenv("PROGRAMDATA")
	if programData == "" {
		return "", fmt.Errorf("PROGRAMDATA is required for the installation control token")
	}
	return filepath.Join(programData, "DobbyVPN", "control.token"), nil
}

func secureControlTokenFile(path string) error {
	account, err := controlTokenUser()
	if err != nil {
		return err
	}
	userSID, _, _, err := windows.LookupSID("", account)
	if err != nil {
		return fmt.Errorf("resolve installed-user SID: %w", err)
	}
	systemSID, err := windows.StringToSid("S-1-5-18")
	if err != nil {
		return err
	}
	return setExactACL(path, []*windows.SID{systemSID, userSID}, windows.GENERIC_READ|windows.GENERIC_WRITE)
}

// SecureInstalledUserPath replaces inherited permissions with an explicit ACL
// for only SYSTEM and the installed user. Directories propagate that ACL to
// children; files grant both identities full access.
func SecureInstalledUserPath(path string) error {
	account, err := controlTokenUser()
	if err != nil {
		return err
	}
	userSID, _, _, err := windows.LookupSID("", account)
	if err != nil {
		return err
	}
	systemSID, err := windows.StringToSid("S-1-5-18")
	if err != nil {
		return err
	}
	return setExactACL(path, []*windows.SID{systemSID, userSID}, windows.GENERIC_ALL)
}

// VerifyInstalledUserPathPermissions rejects inherited or unexpected ACL
// entries. It intentionally shares the same identity policy as the control
// token: the installed user and SYSTEM are the only permitted principals.
func VerifyInstalledUserPathPermissions(path string) error {
	return verifyInstalledUserACL(path)
}

// SecureExplicitUserPath protects a caller-selected runtime path for the
// identity running this process and SYSTEM. Unlike installed paths, this
// policy intentionally does not depend on the configured installed-user
// identity: elevated diagnostics and test harnesses can run as SYSTEM.
func SecureExplicitUserPath(path string) error {
	currentSID, err := currentProcessUserSID()
	if err != nil {
		return err
	}
	systemSID, err := windows.StringToSid("S-1-5-18")
	if err != nil {
		return err
	}
	allowed := []*windows.SID{systemSID}
	if !currentSID.Equals(systemSID) {
		allowed = append(allowed, currentSID)
	}
	return setExactACL(path, allowed, windows.GENERIC_ALL)
}

// setExactACL replaces the DACL atomically. In particular, it does not use
// icacls /inheritance:r, which can preserve inherited ACEs as explicit entries
// on hosted Windows runners.
func setExactACL(path string, allowed []*windows.SID, permissions windows.ACCESS_MASK) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	inheritance := uint32(windows.NO_INHERITANCE)
	if info.IsDir() {
		inheritance = windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT
	}
	entries := make([]windows.EXPLICIT_ACCESS, 0, len(allowed))
	for _, sid := range allowed {
		entries = append(entries, windows.EXPLICIT_ACCESS{
			AccessPermissions: permissions,
			AccessMode:        windows.GRANT_ACCESS,
			Inheritance:       inheritance,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_USER,
				TrusteeValue: windows.TrusteeValueFromSID(sid),
			},
		})
	}
	acl, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		return fmt.Errorf("build explicit runtime path ACL: %w", err)
	}
	if err := windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		acl,
		nil,
	); err != nil {
		return fmt.Errorf("set explicit runtime path ACL: %w", err)
	}
	return nil
}

// VerifyExplicitUserPathPermissions accepts only SYSTEM and the current
// process identity. ACL inheritance and every other principal are rejected.
func VerifyExplicitUserPathPermissions(path string) error {
	currentSID, err := currentProcessUserSID()
	if err != nil {
		return err
	}
	systemSID, err := windows.StringToSid("S-1-5-18")
	if err != nil {
		return err
	}
	allowed := []*windows.SID{systemSID}
	if !currentSID.Equals(systemSID) {
		allowed = append(allowed, currentSID)
	}
	return verifyExactACL(path, allowed, "explicit runtime path", true)
}

func verifyControlTokenPermissions(path string) error {
	return verifyInstalledUserACL(path)
}

func verifyInstalledUserACL(path string) error {
	account, err := controlTokenUser()
	if err != nil {
		return err
	}
	userSID, _, _, err := windows.LookupSID("", account)
	if err != nil {
		return fmt.Errorf("resolve installed-user SID: %w", err)
	}
	systemSID, err := windows.StringToSid("S-1-5-18")
	if err != nil {
		return err
	}
	return verifyExactACL(path, []*windows.SID{systemSID, userSID}, "protected path", false)
}

func verifyExactACL(path string, allowed []*windows.SID, description string, allowDuplicateAllowedEntries bool) error {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("read %s ACL: %w", description, err)
	}
	if sd == nil {
		return fmt.Errorf("%s has no security descriptor", description)
	}
	control, _, err := sd.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		return fmt.Errorf("%s ACL inheritance is not disabled", description)
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil || (!allowDuplicateAllowedEntries && int(acl.AceCount) != len(allowed)) {
		if err != nil {
			return fmt.Errorf("read %s DACL: %w", description, err)
		}
		if acl == nil {
			return fmt.Errorf("%s has no DACL", description)
		}
		return fmt.Errorf("%s ACL contains %d entries; expected %d", description, acl.AceCount, len(allowed))
	}
	found := make([]bool, len(allowed))
	for index := uint32(0); index < uint32(acl.AceCount); index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, index, &ace); err != nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return fmt.Errorf("%s ACL contains an unsupported entry", description)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		matched := false
		for allowedIndex, allowedSID := range allowed {
			if sid.Equals(allowedSID) {
				if found[allowedIndex] && !allowDuplicateAllowedEntries {
					return fmt.Errorf("%s ACL repeats an identity", description)
				}
				found[allowedIndex] = true
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("%s ACL grants access to an unexpected identity", description)
		}
	}
	for _, present := range found {
		if !present {
			return fmt.Errorf("%s ACL is missing an expected identity", description)
		}
	}
	return nil
}

func currentProcessUserSID() (*windows.SID, error) {
	token := windows.GetCurrentProcessToken()
	user, err := token.GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("resolve current process SID: %w", err)
	}
	if user == nil || user.User.Sid == nil {
		return nil, fmt.Errorf("current process SID is unavailable")
	}
	return user.User.Sid, nil
}

func controlTokenUser() (string, error) {
	account := strings.TrimSpace(os.Getenv("DOBBYVPN_CONTROL_TOKEN_USER"))
	if account == "" {
		current, err := user.Current()
		if err != nil {
			return "", fmt.Errorf("resolve current Windows user: %w", err)
		}
		account = current.Username
	}
	upper := strings.ToUpper(account)
	if account == "" || upper == "SYSTEM" || upper == `NT AUTHORITY\SYSTEM` {
		return "", fmt.Errorf("Windows installed-user identity is unavailable for the control token ACL")
	}
	return account, nil
}
