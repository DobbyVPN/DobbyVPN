//go:build windows

package executor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
