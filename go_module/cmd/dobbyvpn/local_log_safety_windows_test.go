//go:build windows

package main

import (
	"os"
	"path/filepath"
	"testing"

	"go_module/desktop_exports/controlplane"
)

func TestWindowsActiveLogUsesExplicitOwnerOnlyACL(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".dobbyvpn", "app_logs.txt")
	if err := controlplane.SecureExplicitUserPath(home); err != nil {
		t.Fatal(err)
	}
	if err := clearLocalLogFileAtBase(path, home); err != nil {
		t.Fatal(err)
	}
	if err := controlplane.VerifyExplicitUserPathPermissions(filepath.Dir(path)); err != nil {
		t.Fatalf("log directory ACL: %v", err)
	}
	if err := controlplane.VerifyExplicitUserPathPermissions(path); err != nil {
		t.Fatalf("log file ACL: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("active log missing after ACL setup: %v", err)
	}
}
