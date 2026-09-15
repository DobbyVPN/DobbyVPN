//go:build dobbyvpn_test_seams

package runtime

import (
	"context"
	"testing"

	"go_module/sessionapi"
)

func TestBuildLocalHealthFaultSeamIsDeterministic(t *testing.T) {
	t.Setenv(testHealthFaultAfterEnv, "1")
	o := options(&recorded{})
	initialCalls := 0
	o.InitialReadiness = func(context.Context, sessionapi.SessionRef) error {
		initialCalls++
		return nil
	}
	liveHealthCalls := 0
	o.ConnectedHealth = func(context.Context, sessionapi.SessionRef) error {
		liveHealthCalls++
		return nil
	}
	r := New(o).(*runtime)

	if err := r.options.InitialReadiness(context.Background(), sessionapi.SessionRef{Generation: 1}); err != nil {
		t.Fatalf("initial readiness was faulted: %v", err)
	}
	if initialCalls != 1 {
		t.Fatalf("initial readiness calls=%d, want 1", initialCalls)
	}
	if err := r.options.ConnectedHealth(context.Background(), sessionapi.SessionRef{Generation: 1}); err != nil {
		t.Fatalf("first monitored check failed: %v", err)
	}
	if err := r.options.ConnectedHealth(context.Background(), sessionapi.SessionRef{Generation: 1}); err == nil {
		t.Fatal("second monitored check unexpectedly succeeded")
	}
	if liveHealthCalls != 0 {
		t.Fatalf("build-local seam invoked the live health probe %d times", liveHealthCalls)
	}
}
