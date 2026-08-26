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
	return setExactACL(path, []*windows.SID{systemSID, userSID}, fileAllAccess)
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
	return setExactACL(path, allowed, fileAllAccess)
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
	return verifyExactACL(path, allowed, currentSID, fileAllAccess, "explicit runtime path")
}

// VerifyUserConfigBasePermissions verifies the existing user-home/config base
// before a caller creates any runtime descendants. The current process, SYSTEM,
// and built-in Administrators are trusted writers; an allow ACE for any other
// identity is accepted only when it is read-only. This intentionally permits a
// normal read-only inherited ACE while rejecting write, delete, ACL, owner,
// generic-write, and generic-all grants that could redirect descendant setup.
func VerifyUserConfigBasePermissions(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect user config base: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("user config base is a reparse point")
	}
	if !info.IsDir() {
		return fmt.Errorf("user config base is not a directory")
	}
	sd, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		return fmt.Errorf("read user config base ACL: %w", err)
	}
	if sd == nil {
		return fmt.Errorf("user config base has no security descriptor")
	}
	if !sd.IsValid() {
		return fmt.Errorf("user config base security descriptor is invalid")
	}
	control, _, err := sd.Control()
	if err != nil {
		return fmt.Errorf("read user config base security descriptor control: %w", err)
	}
	if control&windows.SE_OWNER_DEFAULTED != 0 {
		return fmt.Errorf("user config base owner is defaulted")
	}
	if control&windows.SE_DACL_PRESENT == 0 {
		return fmt.Errorf("user config base has no DACL")
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil {
		if err != nil {
			return fmt.Errorf("read user config base DACL: %w", err)
		}
		return fmt.Errorf("user config base has no DACL")
	}
	trusted, err := trustedBaseWriterSIDs()
	if err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	if err != nil || owner == nil || !containsSID(trusted, owner) {
		if err != nil {
			return fmt.Errorf("read user config base owner: %w", err)
		}
		return fmt.Errorf("user config base owner is not a trusted writer")
	}
	for index := uint32(0); index < uint32(acl.AceCount); index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, index, &ace); err != nil {
			return fmt.Errorf("read user config base ACE %d: %w", index, err)
		}
		if ace == nil {
			return fmt.Errorf("read user config base ACE %d: empty entry", index)
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return fmt.Errorf("user config base ACE %d has an unsupported type", index)
		}
		if ace.Header.AceFlags&^uint8(windows.VALID_INHERIT_FLAGS) != 0 {
			return fmt.Errorf("user config base ACE %d has unsupported flags", index)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if sid == nil || !sid.IsValid() {
			return fmt.Errorf("user config base ACE %d has an invalid identity", index)
		}
		if containsSID(trusted, sid) {
			continue
		}
		if hasUntrustedWriteAccess(ace.Mask) {
			return fmt.Errorf("user config base grants write-capable access to an untrusted identity")
		}
	}
	return nil
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
	return verifyExactACL(path, []*windows.SID{systemSID, userSID}, userSID, fileAllAccess, "protected path")
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
	// SetEntriesInAcl maps generic rights to the file-specific mask stored in
	// the ACE. Keep the expected mask explicit per path so a broader ACL cannot
	// satisfy this verifier by accident.
	return verifyExactACL(
		path,
		[]*windows.SID{systemSID, userSID},
		userSID,
		fileControlTokenAccess,
		"control token",
	)
}

func verifyExactACL(path string, allowed []*windows.SID, expectedOwner *windows.SID, expectedMask windows.ACCESS_MASK, description string) error {
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
	if control&windows.SE_OWNER_DEFAULTED != 0 {
		return fmt.Errorf("%s owner is defaulted", description)
	}
	if control&windows.SE_DACL_DEFAULTED != 0 {
		return fmt.Errorf("%s DACL is defaulted", description)
	}
	if control&windows.SE_DACL_PRESENT == 0 {
		return fmt.Errorf("%s has no DACL", description)
	}
	owner, _, err := sd.Owner()
	if err != nil || owner == nil || expectedOwner == nil || !owner.Equals(expectedOwner) {
		if err != nil {
			return fmt.Errorf("read %s owner: %w", description, err)
		}
		return fmt.Errorf("%s owner is not the expected identity", description)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		return fmt.Errorf("%s ACL inheritance is not disabled", description)
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

func trustedBaseWriterSIDs() ([]*windows.SID, error) {
	current, err := currentProcessUserSID()
	if err != nil {
		return nil, err
	}
	system, err := windows.StringToSid("S-1-5-18")
	if err != nil {
		return nil, err
	}
	admins, err := windows.StringToSid("S-1-5-32-544")
	if err != nil {
		return nil, err
	}
	return []*windows.SID{current, system, admins}, nil
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
	fileAllAccess          windows.ACCESS_MASK = windows.STANDARD_RIGHTS_REQUIRED | windows.SYNCHRONIZE | windows.SPECIFIC_RIGHTS_ALL

	fileDeleteChild windows.ACCESS_MASK = 0x00000040

	// Windows exposes a 32-bit access mask. Every bit is classified here so
	// an untrusted allow ACE with a future/reserved bit fails closed instead of
	// being mistaken for read-only access.
	knownFileAccessMask windows.ACCESS_MASK = windows.FILE_READ_DATA |
		windows.FILE_WRITE_DATA |
		windows.FILE_APPEND_DATA |
		windows.FILE_READ_EA |
		windows.FILE_WRITE_EA |
		windows.FILE_EXECUTE |
		fileDeleteChild |
		windows.FILE_READ_ATTRIBUTES |
		windows.FILE_WRITE_ATTRIBUTES |
		windows.DELETE |
		windows.READ_CONTROL |
		windows.WRITE_DAC |
		windows.WRITE_OWNER |
		windows.SYNCHRONIZE |
		windows.ACCESS_SYSTEM_SECURITY |
		windows.MAXIMUM_ALLOWED |
		windows.GENERIC_READ |
		windows.GENERIC_WRITE |
		windows.GENERIC_EXECUTE |
		windows.GENERIC_ALL

	untrustedWriteAccessMask windows.ACCESS_MASK = windows.FILE_WRITE_DATA |
		windows.FILE_APPEND_DATA |
		windows.FILE_WRITE_EA |
		fileDeleteChild |
		windows.FILE_WRITE_ATTRIBUTES |
		windows.DELETE |
		windows.WRITE_DAC |
		windows.WRITE_OWNER |
		windows.ACCESS_SYSTEM_SECURITY |
		windows.GENERIC_WRITE |
		windows.GENERIC_ALL |
		windows.MAXIMUM_ALLOWED
)

func hasUntrustedWriteAccess(mask windows.ACCESS_MASK) bool {
	return mask&untrustedWriteAccessMask != 0 || mask&^knownFileAccessMask != 0
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
