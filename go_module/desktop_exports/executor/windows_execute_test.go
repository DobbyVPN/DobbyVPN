//go:build windows

package executor

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"go_module/desktop_exports/controlplane"
	"go_module/log"
)

func TestExplicitLogPathMustRemainUnderTemporaryRoot(t *testing.T) {
	root := filepath.Join(`C:\Users\tester\AppData\Local\Temp`, "dobby")
	inside := filepath.Join(root, "session", "service.log")
	if got, err := secureExplicitLogPath(root, inside); err != nil || got != inside {
		t.Fatalf("inside path = %q, %v", got, err)
	}
	outside := filepath.Join(root, "..", "escaped.log")
	if _, err := secureExplicitLogPath(root, outside); err == nil {
		t.Fatal("outside explicit log path was accepted")
	}
}

func TestExplicitLogPathUsesRestrictedWindowsACL(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "session")
	path := filepath.Join(parent, "service.log")
	t.Setenv("DOBBY_LOG_PATH", path)
	t.Cleanup(func() {
		if err := log.Close(); err != nil {
			t.Errorf("close test logger: %v", err)
		}
	})

	if err := initExplicitLocalLog(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	if err := controlplane.VerifyExplicitUserPathPermissions(parent); err != nil {
		t.Fatalf("log directory ACL: %v", err)
	}
	if err := controlplane.VerifyExplicitUserPathPermissions(path); err != nil {
		t.Fatalf("log file ACL: %v", err)
	}
}

func TestPrecreatedLogHandleAppendsWithoutTruncating(t *testing.T) {
	path := filepath.Join(t.TempDir(), "service.log")
	const seed = "seed\n"
	const appended = "appended\n"
	if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := openPrecreatedAppendLog(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(appended); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != seed+appended {
		t.Fatalf("precreated log = %q, want %q", contents, seed+appended)
	}
}

func TestPrecreatedLogInitializationPreservesSeed(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "session")
	if err := os.MkdirAll(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parent, "service.log")
	const seed = "seed\n"
	if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOBBY_LOG_ROOT", parent)
	t.Setenv("DOBBY_LOG_PATH", path)
	t.Setenv("DOBBY_LOG_PRECREATED", "1")
	t.Cleanup(func() {
		if err := log.Close(); err != nil {
			t.Errorf("close test logger: %v", err)
		}
	})

	if err := initExplicitLocalLog(); err != nil {
		t.Fatal(err)
	}
	log.Debugf("TEST", "precreated append marker")
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(contents), seed) || len(contents) == len(seed) {
		t.Fatalf("precreated log was not appended: %q", contents)
	}
}

func TestExplicitLogPathCorrectsUnsafeInheritedACL(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "session")
	if err := os.MkdirAll(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("icacls", parent, "/grant", "*S-1-1-0:(OI)(CI)(F)").Run(); err != nil {
		t.Fatal(err)
	}
	if err := controlplane.VerifyExplicitUserPathPermissions(parent); err == nil {
		t.Fatal("unexpected principal was accepted before hardening")
	}

	path := filepath.Join(parent, "service.log")
	t.Setenv("DOBBY_LOG_PATH", path)
	t.Cleanup(func() {
		if err := log.Close(); err != nil {
			t.Errorf("close test logger: %v", err)
		}
	})
	if err := initExplicitLocalLog(); err != nil {
		t.Fatal(err)
	}
	if err := controlplane.VerifyExplicitUserPathPermissions(parent); err != nil {
		t.Fatalf("corrected log directory ACL: %v", err)
	}
	if err := controlplane.VerifyExplicitUserPathPermissions(path); err != nil {
		t.Fatalf("corrected log file ACL: %v", err)
	}
}

func TestExplicitLogPathRejectsReparseTraversal(t *testing.T) {
	target := filepath.Join(t.TempDir(), "target")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(os.TempDir(), "dobbyvpn-log-link-"+filepath.Base(t.TempDir()))
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("creating Windows symlink requires host support: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(link) })
	t.Setenv("DOBBY_LOG_PATH", filepath.Join(link, "service.log"))

	if err := initExplicitLocalLog(); err == nil {
		t.Fatal("reparse-point traversal was accepted")
	}
}
