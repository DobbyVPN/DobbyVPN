//go:build windows

package controlplane

import (
	"testing"

	"golang.org/x/sys/windows"
)

func assertOwnerOnlyTokenPermissions(t *testing.T, path string) {
	t.Helper()
	if err := verifyControlTokenPermissions(path); err != nil {
		t.Fatalf("token ACL: %v", err)
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
