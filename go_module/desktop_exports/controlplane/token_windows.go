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
	return setExactACL(path, []*windows.SID{systemSID, userSID}, fileControlTokenAccess)
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

func verifyControlTokenPermissions(path string) error {
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
	administratorsSID, err := windows.StringToSid("S-1-5-32-544")
	if err != nil {
		return err
	}
	// SetEntriesInAcl maps generic rights to the file-specific mask stored in
	// the ACE. Keep the expected mask explicit per path so a broader ACL cannot
	// satisfy this verifier by accident.
	// The service can create the token as SYSTEM, while the desktop process can
	// create it as the configured installed user. These are the same two
	// identities already required by the exact token DACL. An elevated Windows
	// process may inherit BUILTIN\\Administrators as the descriptor owner even
	// when its effective user is the configured account; that trusted owner does
	// not become an ACL principal and therefore does not widen token access.
	return verifyExactACLWithOwners(
		path,
		[]*windows.SID{systemSID, userSID},
		[]*windows.SID{systemSID, userSID, administratorsSID},
		fileControlTokenAccess,
		"control token",
	)
}

func verifyExactACL(path string, allowed []*windows.SID, expectedOwner *windows.SID, expectedMask windows.ACCESS_MASK, description string) error {
	return verifyExactACLWithOwners(path, allowed, []*windows.SID{expectedOwner}, expectedMask, description)
}

func verifyExactACLWithOwners(path string, allowed, expectedOwners []*windows.SID, expectedMask windows.ACCESS_MASK, description string) error {
	sd, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		return fmt.Errorf("read %s ACL: %w", description, err)
	}
	if sd == nil {
		return fmt.Errorf("%s has no security descriptor", description)
	}
	if !sd.IsValid() {
		return fmt.Errorf("%s security descriptor is invalid", description)
	}
	control, _, err := sd.Control()
	if err != nil {
		return fmt.Errorf("read %s security descriptor control: %w", description, err)
	}
	if err := validateExactACLControl(control, description); err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	if err != nil || !matchesExpectedOwner(owner, expectedOwners) {
		if err != nil {
			return fmt.Errorf("read %s owner: %w", description, err)
		}
		return fmt.Errorf("%s owner is not the expected identity", description)
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil || int(acl.AceCount) != len(allowed) {
		if err != nil {
			return fmt.Errorf("read %s DACL: %w", description, err)
		}
		if acl == nil {
			return fmt.Errorf("%s has no DACL", description)
		}
		return fmt.Errorf("%s ACL contains %d entries; expected %d", description, acl.AceCount, len(allowed))
	}
	found := make([]bool, len(allowed))
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect %s type: %w", description, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is a reparse point", description)
	}
	expectedFlags := uint8(windows.NO_INHERITANCE)
	if info.IsDir() {
		expectedFlags = windows.OBJECT_INHERIT_ACE | windows.CONTAINER_INHERIT_ACE
	}
	for index := uint32(0); index < uint32(acl.AceCount); index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, index, &ace); err != nil || ace == nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return fmt.Errorf("%s ACL contains an unsupported entry", description)
		}
		if ace.Header.AceFlags&^uint8(windows.VALID_INHERIT_FLAGS) != 0 {
			return fmt.Errorf("%s ACL contains unsupported inheritance flags", description)
		}
		if ace.Mask != expectedMask || ace.Header.AceFlags != expectedFlags {
			return fmt.Errorf("%s ACL contains an entry with unexpected access mask or inheritance", description)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if sid == nil || !sid.IsValid() {
			return fmt.Errorf("%s ACL contains an invalid identity", description)
		}
		matched := false
		for allowedIndex, allowedSID := range allowed {
			if sid.Equals(allowedSID) {
				if found[allowedIndex] {
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

func validateExactACLControl(control windows.SECURITY_DESCRIPTOR_CONTROL, description string) error {
	// OWNER_DEFAULTED and DACL_DEFAULTED describe descriptor provenance, not
	// access semantics. The owner identity and exact protected DACL below are
	// the security policy; defaulted provenance does not weaken either check.
	if control&windows.SE_DACL_PRESENT == 0 {
		return fmt.Errorf("%s has no DACL", description)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		return fmt.Errorf("%s ACL inheritance is not disabled", description)
	}
	return nil
}

func matchesExpectedOwner(owner *windows.SID, expectedOwners []*windows.SID) bool {
	return containsSID(expectedOwners, owner)
}

func containsSID(identities []*windows.SID, candidate *windows.SID) bool {
	for _, identity := range identities {
		if identity != nil && candidate != nil && identity.Equals(candidate) {
			return true
		}
	}
	return false
}

const (
	// SetEntriesInAcl expands GENERIC_* rights into the corresponding
	// file-specific masks when it builds an ACL. Use those masks for both
	// writing and verification; comparing the generic bits would reject a
	// correctly secured file on Windows.
	fileControlTokenAccess windows.ACCESS_MASK = windows.FILE_GENERIC_READ | windows.FILE_GENERIC_WRITE
)

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
