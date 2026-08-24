//go:build !dobbyvpn_test_seams

package runtime

import (
	"context"
	"strings"
	"testing"
	"time"

	v1 "go_module/sessionapi/v2"
)

func TestUntaggedBuildIgnoresBothHealthFaultEnvironmentVariables(t *testing.T) {
	legacyName := strings.Join([]string{
		"DOBBYVPN", "HARDENING", "TEST", "FAIL", "HEALTH", "AFTER", "SUCCESSFUL", "CHECKS",
	}, "_")
	seamName := strings.Join([]string{
		"DOBBYVPN", "TEST", "HEALTH", "FAULT", "AFTER", "SUCCESSFUL", "CHECKS",
	}, "_")
	t.Setenv(legacyName, "1")
	t.Setenv(seamName, "1")
	o := options(&recorded{})
	checks := 0
	o.ConnectedHealth = func(context.Context, v1.SessionRef) error {
		checks++
		return nil
	}
	r := New(o).(*runtime)
	if r.options.HealthInterval != 10*time.Second || r.options.HealthFailureThreshold != 3 {
		t.Fatalf("untagged environment changed product defaults: interval=%s threshold=%d", r.options.HealthInterval, r.options.HealthFailureThreshold)
	}
	if err := r.options.ConnectedHealth(context.Background(), v1.SessionRef{Generation: 1}); err != nil {
		t.Fatal(err)
	}
	if checks != 1 {
		t.Fatalf("untagged health callback calls=%d, want 1", checks)
	}
}
