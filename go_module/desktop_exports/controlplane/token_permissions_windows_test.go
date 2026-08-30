//go:build windows

package controlplane

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func assertOwnerOnlyTokenPermissions(t *testing.T, path string) {
	t.Helper()
	if err := verifyControlTokenPermissions(path); err != nil {
		t.Fatalf("token ACL: %v", err)
	}
}

func TestInstalledUserPathPolicyRemainsStrict(t *testing.T) {
	path := filepath.Join(t.TempDir(), "installed")
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := SecureInstalledUserPath(path); err != nil {
		t.Fatal(err)
	}
	if err := VerifyInstalledUserPathPermissions(path); err != nil {
		t.Fatalf("strict installed ACL: %v", err)
	}
	if err := exec.Command("icacls", path, "/grant", "*S-1-1-0:(OI)(CI)(R)").Run(); err != nil {
		t.Fatal(err)
	}
	if err := VerifyInstalledUserPathPermissions(path); err == nil {
		t.Fatal("installed path accepted an unexpected principal")
	}
}

func TestControlTokenOwnerPolicyAcceptsOnlyTrustedOwners(t *testing.T) {
	systemSID, err := windows.StringToSid("S-1-5-18")
	if err != nil {
		t.Fatal(err)
	}
	installedUserSID, err := windows.StringToSid("S-1-5-21-1111111111-2222222222-3333333333-1001")
	if err != nil {
		t.Fatal(err)
	}
	administratorsSID, err := windows.StringToSid("S-1-5-32-544")
	if err != nil {
		t.Fatal(err)
	}
	thirdPartySID, err := windows.StringToSid("S-1-1-0")
	if err != nil {
		t.Fatal(err)
	}
	trustedOwners := []*windows.SID{systemSID, installedUserSID, administratorsSID}

	for name, owner := range map[string]*windows.SID{
		"SYSTEM":         systemSID,
		"installed user": installedUserSID,
		"Administrators": administratorsSID,
	} {
		t.Run(name, func(t *testing.T) {
			if !matchesExpectedOwner(owner, trustedOwners) {
				t.Fatalf("trusted owner %v was rejected", owner)
			}
		})
	}
	if matchesExpectedOwner(thirdPartySID, trustedOwners) {
		t.Fatal("third-party owner was accepted")
	}
	if matchesExpectedOwner(nil, trustedOwners) {
		t.Fatal("missing owner was accepted")
	}
}

func TestExactACLControlPolicyIgnoresDefaultedProvenance(t *testing.T) {
	for name, control := range map[string]windows.SECURITY_DESCRIPTOR_CONTROL{
		"neither defaulted":    windows.SE_DACL_PRESENT | windows.SE_DACL_PROTECTED,
		"owner defaulted only": windows.SE_OWNER_DEFAULTED | windows.SE_DACL_PRESENT | windows.SE_DACL_PROTECTED,
		"DACL defaulted only":  windows.SE_DACL_DEFAULTED | windows.SE_DACL_PRESENT | windows.SE_DACL_PROTECTED,
		"both defaulted":       windows.SE_OWNER_DEFAULTED | windows.SE_DACL_DEFAULTED | windows.SE_DACL_PRESENT | windows.SE_DACL_PROTECTED,
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateExactACLControl(control, "test ACL"); err != nil {
				t.Fatalf("control policy rejected valid descriptor: %v", err)
			}
		})
	}

	for name, control := range map[string]windows.SECURITY_DESCRIPTOR_CONTROL{
		"absent DACL":      windows.SE_OWNER_DEFAULTED | windows.SE_DACL_DEFAULTED | windows.SE_DACL_PROTECTED,
		"unprotected DACL": windows.SE_OWNER_DEFAULTED | windows.SE_DACL_DEFAULTED | windows.SE_DACL_PRESENT,
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateExactACLControl(control, "test ACL"); err == nil {
				t.Fatal("control policy accepted an invalid descriptor")
			}
		})
	}
}

func TestUntrustedBaseAccessMaskClassification(t *testing.T) {
	for _, mask := range []windows.ACCESS_MASK{
		windows.GENERIC_WRITE,
		windows.GENERIC_ALL,
		windows.FILE_WRITE_DATA,
		windows.FILE_APPEND_DATA,
		windows.FILE_WRITE_ATTRIBUTES,
		windows.FILE_WRITE_EA,
		windows.DELETE,
		windows.WRITE_DAC,
		windows.WRITE_OWNER,
		windows.ACCESS_SYSTEM_SECURITY,
		windows.MAXIMUM_ALLOWED,
		fileDeleteChild,
	} {
		if !hasUntrustedWriteAccess(mask) {
			t.Fatalf("write-capable mask %#x was classified as read-only", mask)
		}
	}
	for _, mask := range []windows.ACCESS_MASK{windows.GENERIC_READ, windows.FILE_READ_DATA, windows.READ_CONTROL} {
		if hasUntrustedWriteAccess(mask) {
			t.Fatalf("read-only mask %#x was classified as write-capable", mask)
		}
	}
	if !hasUntrustedWriteAccess(windows.ACCESS_MASK(0x00800000)) {
		t.Fatal("reserved access-mask bit was classified as read-only")
	}
}
