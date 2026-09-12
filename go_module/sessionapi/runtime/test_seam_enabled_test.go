//go:build dobbyvpn_test_seams

package runtime

import (
	"context"
	"testing"

	v1 "go_module/sessionapi/v2"
)

func TestBuildLocalHealthFaultSeamIsDeterministic(t *testing.T) {
	t.Setenv(testHealthFaultAfterEnv, "1")
	o := options(&recorded{})
	initialCalls := 0
	o.InitialReadiness = func(context.Context, v1.SessionRef) error {
		initialCalls++
		return nil
	}
	liveHealthCalls := 0
	o.ConnectedHealth = func(context.Context, v1.SessionRef) error {
		liveHealthCalls++
		return nil
	}
	r := New(o).(*runtime)

	if err := r.options.InitialReadiness(context.Background(), v1.SessionRef{Generation: 1}); err != nil {
		t.Fatalf("initial readiness was faulted: %v", err)
	}
	if initialCalls != 1 {
		t.Fatalf("initial readiness calls=%d, want 1", initialCalls)
	}
	if err := r.options.ConnectedHealth(context.Background(), v1.SessionRef{Generation: 1}); err != nil {
		t.Fatalf("first monitored check failed: %v", err)
	}
	if err := r.options.ConnectedHealth(context.Background(), v1.SessionRef{Generation: 1}); err == nil {
		t.Fatal("second monitored check unexpectedly succeeded")
	}
	if liveHealthCalls != 0 {
		t.Fatalf("build-local seam invoked the live health probe %d times", liveHealthCalls)
	}
}
