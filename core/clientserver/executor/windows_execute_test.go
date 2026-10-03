//go:build windows

package executor

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"core/log"
)

type recordingDesktopListener struct {
	events *[]string
}

func (listener *recordingDesktopListener) Accept() (net.Conn, error) {
	return nil, net.ErrClosed
}

func (listener *recordingDesktopListener) Close() error {
	*listener.events = append(*listener.events, "close")
	return nil
}

func (listener *recordingDesktopListener) Addr() net.Addr {
	return &net.TCPAddr{}
}

func TestPrepareDesktopControlClaimsPipeBeforeRecovery(t *testing.T) {
	var events []string
	listener := &recordingDesktopListener{events: &events}
	prepared, err := prepareDesktopControlWith(
		func() (net.Listener, error) {
			events = append(events, "listen")
			return listener, nil
		},
		func() error {
			events = append(events, "interrupted-state")
			return nil
		},
		func() error {
			events = append(events, "stale-firewall")
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if prepared != listener {
		t.Fatal("startup did not return the listener whose pipe it claimed")
	}
	want := []string{"listen", "interrupted-state", "stale-firewall"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("startup order=%v, want=%v", events, want)
	}
}

func TestPrepareDesktopControlClosesClaimedPipeOnRecoveryFailure(t *testing.T) {
	var events []string
	listener := &recordingDesktopListener{events: &events}
	recoveryErr := errors.New("firewall recovery failed")
	prepared, err := prepareDesktopControlWith(
		func() (net.Listener, error) {
			events = append(events, "listen")
			return listener, nil
		},
		func() error {
			events = append(events, "interrupted-state")
			return nil
		},
		func() error {
			events = append(events, "stale-firewall")
			return recoveryErr
		},
	)
	if prepared != nil || !errors.Is(err, recoveryErr) {
		t.Fatalf("prepared listener=%v err=%v, want recovery failure", prepared, err)
	}
	want := []string{"listen", "interrupted-state", "stale-firewall", "close"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("failure cleanup order=%v, want=%v", events, want)
	}
}

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

	if prepared, err := prepareLocalLogPath(); err != nil {
		t.Fatal(err)
	} else if err := log.SetPath(prepared); err != nil {
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
