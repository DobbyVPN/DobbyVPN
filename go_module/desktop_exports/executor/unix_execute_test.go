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
