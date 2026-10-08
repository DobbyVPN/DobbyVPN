//go:build dobbyvpn_test_seams

package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"core/sessionapi"
)

func TestBuildLocalHealthFaultSeamIsDeterministic(t *testing.T) {
	t.Cleanup(resetTestSeamsForTest)
	t.Setenv(testHealthFaultAfterEnv, "1")
	o := options(&recorded{})
	initialCalls := 0
	o.InitialReadiness = func(context.Context, sessionapi.SessionRef, string) error {
		initialCalls++
		return nil
	}
	liveHealthCalls := 0
	o.ConnectedHealth = func(context.Context, sessionapi.SessionRef, string) error {
		liveHealthCalls++
		return nil
	}
	r := New(o).(*runtime)

	if err := r.options.InitialReadiness(context.Background(), sessionapi.SessionRef{Generation: 1}, ""); err != nil {
		t.Fatalf("initial readiness was faulted: %v", err)
	}
	if initialCalls != 1 {
		t.Fatalf("initial readiness calls=%d, want 1", initialCalls)
	}
	if err := r.options.ConnectedHealth(context.Background(), sessionapi.SessionRef{Generation: 1}, ""); err != nil {
		t.Fatalf("first monitored check failed: %v", err)
	}
	if err := r.options.ConnectedHealth(context.Background(), sessionapi.SessionRef{Generation: 1}, ""); err == nil {
		t.Fatal("second monitored check unexpectedly succeeded")
	}
	if liveHealthCalls != 0 {
		t.Fatalf("build-local seam invoked the live health probe %d times", liveHealthCalls)
	}
}

func TestRecoveryStopControlUsesRealChecksAndHoldsOnlyRecoveryGeneration(t *testing.T) {
	resetTestSeamsForTest()
	t.Cleanup(resetTestSeamsForTest)
	readinessCalls := 0
	healthCalls := 0
	o := options(&recorded{})
	o.InitialReadiness = func(context.Context, sessionapi.SessionRef, string) error {
		readinessCalls++
		return nil
	}
	o.ConnectedHealth = func(context.Context, sessionapi.SessionRef, string) error {
		healthCalls++
		return nil
	}
	r := New(o).(*runtime)

	if ArmTestRecoveryStop() {
		t.Fatal("fault armed before the seam was enabled")
	}
	if !EnableTestRecoveryStop() {
		t.Fatal("could not enable the runtime test seam after runtime construction")
	}
	ref := sessionapi.SessionRef{SessionID: "test-session", Generation: 1}
	if err := r.options.InitialReadiness(context.Background(), ref, ""); err != nil {
		t.Fatalf("initial readiness failed: %v", err)
	}
	if err := r.options.ConnectedHealth(context.Background(), ref, ""); err != nil {
		t.Fatalf("pre-arm connected health failed: %v", err)
	}
	if readinessCalls != 1 || healthCalls != 1 {
		t.Fatalf("real checks before arm = readiness:%d health:%d, want 1 each", readinessCalls, healthCalls)
	}
	if !ArmTestRecoveryStop() {
		t.Fatal("could not arm the recovery Stop fault")
	}
	if err := r.options.ConnectedHealth(context.Background(), ref, ""); err == nil {
		t.Fatal("armed connected-health check unexpectedly succeeded")
	}
	if healthCalls != 1 {
		t.Fatalf("injected health fault invoked real health %d times total, want 1", healthCalls)
	}

	// Neither a different session nor the faulting generation may consume the
	// hold intended for this session's next recovery generation.
	for _, wrongRef := range []sessionapi.SessionRef{
		{SessionID: "other-session", Generation: 2},
		{SessionID: ref.SessionID, Generation: ref.Generation},
	} {
		if err := r.options.InitialReadiness(context.Background(), wrongRef, ""); err != nil {
			t.Fatalf("unrelated initial readiness failed for %+v: %v", wrongRef, err)
		}
	}

	recoveryRef := sessionapi.SessionRef{SessionID: ref.SessionID, Generation: ref.Generation + 1}
	recoveryCtx, cancelRecovery := context.WithCancel(context.Background())
	recoveryDone := make(chan error, 1)
	go func() { recoveryDone <- r.options.InitialReadiness(recoveryCtx, recoveryRef, "") }()
	waitForRecoveryStopPhase(t, recoveryStopHolding)
	select {
	case err := <-recoveryDone:
		t.Fatalf("recovery readiness completed before Stop cancellation: %v", err)
	default:
	}
	cancelRecovery()
	select {
	case err := <-recoveryDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("recovery readiness error=%v, want context cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("recovery readiness did not return after cancellation")
	}
	if readinessCalls != 4 {
		t.Fatalf("real readiness calls=%d, want initial, two unrelated, and recovery", readinessCalls)
	}
	stopSeam.mu.Lock()
	phaseAfterStop := stopSeam.phase
	stopSeam.mu.Unlock()
	if phaseAfterStop != recoveryStopEnabled {
		t.Fatalf("test seam phase after Stop cancellation=%d, want enabled", phaseAfterStop)
	}
}

func TestRecoveryStopMarkerArmsOnNewMarkerOnly(t *testing.T) {
	resetTestSeamsForTest()
	t.Cleanup(resetTestSeamsForTest)
	marker := filepath.Join(t.TempDir(), "trigger")
	t.Setenv(testRecoveryStopMarkerEnv, marker)
	readinessCalls := 0
	healthCalls := 0
	o := options(&recorded{})
	o.InitialReadiness = func(context.Context, sessionapi.SessionRef, string) error {
		readinessCalls++
		return nil
	}
	o.ConnectedHealth = func(context.Context, sessionapi.SessionRef, string) error {
		healthCalls++
		return nil
	}
	r := New(o).(*runtime)
	interval, threshold := testHealthMonitorTiming(r.options.HealthInterval, r.options.HealthFailureThreshold)
	if interval != time.Second || threshold != 1 {
		t.Fatalf("enabled marker timing=%s/%d, want 1s/1", interval, threshold)
	}

	ref := sessionapi.SessionRef{SessionID: "marker-session", Generation: 4}
	if err := r.options.InitialReadiness(context.Background(), ref, ""); err != nil {
		t.Fatalf("initial real readiness failed: %v", err)
	}
	if err := r.options.ConnectedHealth(context.Background(), ref, ""); err != nil {
		t.Fatalf("pre-marker real health failed: %v", err)
	}
	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		t.Fatalf("create run-owned marker: %v", err)
	}
	if err := r.options.ConnectedHealth(context.Background(), ref, ""); err == nil {
		t.Fatal("marker did not arm the connected-health fault")
	}
	if healthCalls != 1 {
		t.Fatalf("real health checks=%d, want only the pre-marker check", healthCalls)
	}
	recoveryCtx, cancelRecovery := context.WithCancel(context.Background())
	recoveryDone := make(chan error, 1)
	go func() {
		recoveryDone <- r.options.InitialReadiness(recoveryCtx, sessionapi.SessionRef{
			SessionID: ref.SessionID, Generation: ref.Generation + 1,
		}, "")
	}()
	waitForRecoveryStopPhase(t, recoveryStopHolding)
	if readinessCalls != 2 {
		t.Fatalf("real initial readiness calls=%d, want initial and recovery checks", readinessCalls)
	}
	cancelRecovery()
	select {
	case err := <-recoveryDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("marker recovery readiness error=%v, want context cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("marker recovery readiness did not return after cancellation")
	}
}

func TestRecoveryStopMarkerUsesTaggedLinkerDefault(t *testing.T) {
	resetTestSeamsForTest()
	t.Cleanup(resetTestSeamsForTest)
	t.Setenv(testRecoveryStopMarkerEnv, "")
	originalMarker := TestRecoveryStopMarker
	TestRecoveryStopMarker = filepath.Join(t.TempDir(), "trigger")
	t.Cleanup(func() { TestRecoveryStopMarker = originalMarker })
	r := New(options(&recorded{})).(*runtime)
	interval, threshold := testHealthMonitorTiming(r.options.HealthInterval, r.options.HealthFailureThreshold)
	if interval != time.Second || threshold != 1 {
		t.Fatalf("linker marker timing=%s/%d, want 1s/1", interval, threshold)
	}
}

func TestInvalidRecoveryStopMarkerSetupLeavesNormalHealthActive(t *testing.T) {
	for name, marker := range map[string]func(*testing.T) string{
		"relative path":  func(*testing.T) string { return "relative-trigger" },
		"missing parent": func(t *testing.T) string { return filepath.Join(t.TempDir(), "missing", "trigger") },
		"marker already exists": func(t *testing.T) string {
			path := filepath.Join(t.TempDir(), "trigger")
			if err := os.WriteFile(path, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			return path
		},
	} {
		t.Run(name, func(t *testing.T) {
			resetTestSeamsForTest()
			t.Cleanup(resetTestSeamsForTest)
			t.Setenv(testRecoveryStopMarkerEnv, marker(t))
			checks := 0
			o := options(&recorded{})
			o.ConnectedHealth = func(context.Context, sessionapi.SessionRef, string) error {
				checks++
				return nil
			}
			r := New(o).(*runtime)
			interval, threshold := testHealthMonitorTiming(r.options.HealthInterval, r.options.HealthFailureThreshold)
			if interval != 10*time.Second || threshold != 3 {
				t.Fatalf("invalid marker changed health timing=%s/%d", interval, threshold)
			}
			if ArmTestRecoveryStop() {
				t.Fatal("invalid marker setup unexpectedly enabled fault control")
			}
			if err := r.options.ConnectedHealth(context.Background(), sessionapi.SessionRef{SessionID: "s", Generation: 1}, ""); err != nil {
				t.Fatalf("normal connected health failed: %v", err)
			}
			if checks != 1 {
				t.Fatalf("normal health callback calls=%d, want 1", checks)
			}
		})
	}
}

func waitForRecoveryStopPhase(t *testing.T, phase recoveryStopPhase) {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer deadline.Stop()
	defer ticker.Stop()
	for {
		stopSeam.mu.Lock()
		current := stopSeam.phase
		stopSeam.mu.Unlock()
		if current == phase {
			return
		}
		select {
		case <-deadline.C:
			t.Fatalf("test seam phase=%d, want %d", current, phase)
		case <-ticker.C:
		}
	}
}
