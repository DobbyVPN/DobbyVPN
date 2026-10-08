//go:build !dobbyvpn_test_seams

package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"core/sessionapi"
)

func TestUntaggedBuildIgnoresHealthAndRecoveryFaultEnvironmentVariables(t *testing.T) {
	legacyName := strings.Join([]string{
		"DOBBYVPN", "HARDENING", "TEST", "FAIL", "HEALTH", "AFTER", "SUCCESSFUL", "CHECKS",
	}, "_")
	seamName := strings.Join([]string{
		"DOBBYVPN", "TEST", "HEALTH", "FAULT", "AFTER", "SUCCESSFUL", "CHECKS",
	}, "_")
	t.Setenv(legacyName, "1")
	t.Setenv(seamName, "1")
	marker := filepath.Join(t.TempDir(), "recovery-stop.arm")
	t.Setenv("DOBBYVPN_TEST_RECOVERY_STOP_MARKER", marker)
	o := options(&recorded{})
	checks := 0
	o.ConnectedHealth = func(context.Context, sessionapi.SessionRef, string) error {
		checks++
		return nil
	}
	r := New(o).(*runtime)
	if err := os.WriteFile(marker, []byte("armed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if r.options.HealthInterval != 10*time.Second || r.options.HealthFailureThreshold != 3 {
		t.Fatalf("untagged environment changed product defaults: interval=%s threshold=%d", r.options.HealthInterval, r.options.HealthFailureThreshold)
	}
	if err := r.options.ConnectedHealth(context.Background(), sessionapi.SessionRef{Generation: 1}, ""); err != nil {
		t.Fatal(err)
	}
	if checks != 1 {
		t.Fatalf("untagged health callback calls=%d, want 1", checks)
	}
	interval, threshold := testHealthMonitorTiming(r.options.HealthInterval, r.options.HealthFailureThreshold)
	if interval != 10*time.Second || threshold != 3 {
		t.Fatalf("untagged recovery marker changed monitor timing: interval=%s threshold=%d", interval, threshold)
	}
}
