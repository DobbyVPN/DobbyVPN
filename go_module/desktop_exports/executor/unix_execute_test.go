//go:build !(windows || android || ios)

package executor

import (
	"os"
	"path/filepath"
	"testing"

	"go_module/log"
)

func TestExplicitLocalLogUsesOwnerTemporaryPath(t *testing.T) {
	parent := t.TempDir()
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
	if info, err := os.Stat(path); err != nil {
		t.Fatal(err)
	} else if info.Mode().Perm() != 0o600 {
		t.Fatalf("log mode = %o, want 600", info.Mode().Perm())
	}
}

func TestExplicitLocalLogRejectsPathOutsideTemporaryRoot(t *testing.T) {
	t.Setenv("DOBBY_LOG_PATH", filepath.Join(os.TempDir(), "..", "var", "tmp", "dobbyvpn-outside.log"))
	if err := initExplicitLocalLog(); err == nil {
		t.Fatal("outside explicit log path was accepted")
	}
}

func TestExplicitLocalLogUsesPrecreatedSupervisorFile(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "retained")
	if err := os.Mkdir(parent, 0o750); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parent, "service.log")
	if err := os.WriteFile(path, []byte("supervisor-prefix\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOBBY_LOG_ROOT", root)
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
	if info, err := os.Stat(path); err != nil {
		t.Fatal(err)
	} else if info.Mode().Perm() != 0o640 {
		t.Fatalf("managed log mode = %o, want unchanged 640", info.Mode().Perm())
	}
}

func TestManagedExplicitLogRequiresExistingFileInsideRoot(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DOBBY_LOG_ROOT", root)
	t.Setenv("DOBBY_LOG_PRECREATED", "1")
	t.Setenv("DOBBY_LOG_PATH", filepath.Join(root, "missing.log"))
	if err := initExplicitLocalLog(); err == nil {
		t.Fatal("missing managed log was accepted")
	}

	outside := filepath.Join(t.TempDir(), "service.log")
	if err := os.WriteFile(outside, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOBBY_LOG_PATH", outside)
	if err := initExplicitLocalLog(); err == nil {
		t.Fatal("managed log outside its root was accepted")
	}
}
