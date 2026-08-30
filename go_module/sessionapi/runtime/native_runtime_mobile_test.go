//go:build android || ios

package runtime

import (
	"errors"
	"net"
	"os"
	"testing"
	"time"
)

// These tests exercise the product-owned mobile runtime seam directly. They do
// not require a device, emulator, VPN profile, or test-infrastructure change.
type panicMobileDevice struct{}

func (*panicMobileDevice) Open(int, string) error { panic("test protocol open panic") }
func (*panicMobileDevice) GetProxyAddr() string   { return "127.0.0.1:1" }
func (*panicMobileDevice) GetServerIP() net.IP    { return net.IPv4(192, 0, 2, 1) }
func (*panicMobileDevice) Close() error           { return nil }

type mobileTestTun struct {
	*os.File
	closeErr   error
	closeCalls int
}

func (t *mobileTestTun) Close() error {
	t.closeCalls++
	if t.File != nil {
		_ = t.File.Close()
	}
	return t.closeErr
}

func (t *mobileTestTun) Fd() uintptr { return t.File.Fd() }

func newMobileTestTun(t *testing.T, closeErr error) *mobileTestTun {
	t.Helper()
	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writeEnd.Close() })
	return &mobileTestTun{File: readEnd, closeErr: closeErr}
}

func TestCloseMobileTunAfterEngineRetainsLedgerOnFailure(t *testing.T) {
	want := errors.New("test TUN close failed")
	releaseCalls := 0
	err := closeMobileTunAfterEngine(
		func() error { return want },
		func() { releaseCalls++ },
	)
	if !errors.Is(err, want) {
		t.Fatalf("closeMobileTunAfterEngine error = %v, want %v", err, want)
	}
	if releaseCalls != 0 {
		t.Fatalf("ledger release calls = %d, want 0 after failed close", releaseCalls)
	}
}

func TestCloseMobileTunAfterEngineReleasesOnlyAfterSuccess(t *testing.T) {
	releaseCalls := 0
	err := closeMobileTunAfterEngine(
		func() error { return nil },
		func() { releaseCalls++ },
	)
	if err != nil {
		t.Fatalf("closeMobileTunAfterEngine error = %v", err)
	}
	if releaseCalls != 1 {
		t.Fatalf("ledger release calls = %d, want 1", releaseCalls)
	}
}

func connectMobileBounded(t *testing.T, c *nativeRuntime) error {
	t.Helper()
	result := make(chan error, 1)
	go func() { result <- c.Connect() }()
	select {
	case err := <-result:
		return err
	case <-time.After(time.Second):
		t.Fatal("Connect did not return after a panic")
		return nil
	}
}

func TestMobileConnectPanicRecoveryDoesNotDeadlockAndAllowsNextGeneration(t *testing.T) {
	firstTun := newMobileTestTun(t, nil)
	c := newNativeRuntime(&panicMobileDevice{}, firstTun)

	if err := connectMobileBounded(t, c); err == nil {
		t.Fatal("Connect unexpectedly succeeded after protocol-open panic")
	}
	if got := c.stateValue(); got != stateIdle {
		t.Fatalf("state after successful panic cleanup = %s, want %s", got, stateIdle)
	}
	if c.cleanupErr != nil {
		t.Fatalf("cleanupErr after successful panic cleanup = %v", c.cleanupErr)
	}
	if firstTun.closeCalls != 1 {
		t.Fatalf("TUN close calls after panic cleanup = %d, want 1", firstTun.closeCalls)
	}

	// A clean rollback leaves the adapter unfenced: a later generation can
	// enter Connect instead of blocking on the old mutex or cleanup error.
	secondTun := newMobileTestTun(t, nil)
	c.device = &panicMobileDevice{}
	c.tun = secondTun
	if err := connectMobileBounded(t, c); err == nil {
		t.Fatal("later generation unexpectedly succeeded after forced test panic")
	}
	if got := c.generationValue(); got != 2 {
		t.Fatalf("generation after successful-cleanup retry = %d, want 2", got)
	}
}

func TestMobileConnectPanicRecoveryFencesLaterGenerationWhenCleanupFails(t *testing.T) {
	wantCleanupErr := errors.New("test TUN cleanup failed")
	firstTun := newMobileTestTun(t, wantCleanupErr)
	c := newNativeRuntime(&panicMobileDevice{}, firstTun)

	if err := connectMobileBounded(t, c); !errors.Is(err, wantCleanupErr) {
		t.Fatalf("Connect error = %v, want cleanup error %v", err, wantCleanupErr)
	}
	if got := c.stateValue(); got != stateFailed {
		t.Fatalf("state after failed panic cleanup = %s, want %s", got, stateFailed)
	}
	if firstTun.closeCalls != 1 {
		t.Fatalf("TUN close calls after failed panic cleanup = %d, want 1", firstTun.closeCalls)
	}

	secondTun := newMobileTestTun(t, nil)
	c.device = &panicMobileDevice{}
	c.tun = secondTun
	if err := connectMobileBounded(t, c); !errors.Is(err, wantCleanupErr) {
		t.Fatalf("fenced later-generation error = %v, want cleanup error %v", err, wantCleanupErr)
	}
	if got := c.generationValue(); got != 1 {
		t.Fatalf("generation after incomplete-cleanup retry = %d, want 1", got)
	}
}
