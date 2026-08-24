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
