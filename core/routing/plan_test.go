package routing

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

func TestPlanClosesLeasesInReverseOrderAndOnlyOnce(t *testing.T) {
	plan := NewPlan("generation-17")
	var released []string
	for _, name := range []string{"engine-bypass", "mark-rule", "tun-default"} {
		name := name
		if _, err := plan.Acquire(name, func() error { return nil }, func() error {
			released = append(released, name)
			return nil
		}); err != nil {
			t.Fatalf("Acquire(%s): %v", name, err)
		}
	}

	if err := plan.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := plan.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if want := []string{"tun-default", "mark-rule", "engine-bypass"}; !reflect.DeepEqual(released, want) {
		t.Fatalf("release order = %v, want %v", released, want)
	}
}

func TestPlanDoesNotCreateLeaseWhenAcquisitionFails(t *testing.T) {
	plan := NewPlan("generation-18")
	released := false
	if _, err := plan.Acquire("route", func() error { return errors.New("add denied") }, func() error {
		released = true
		return nil
	}); err == nil {
		t.Fatal("Acquire succeeded")
	}
	if err := plan.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if released {
		t.Fatal("release ran for a resource that was never acquired")
	}
}

func TestPlanRejectsAcquisitionAfterCloseWithoutApplying(t *testing.T) {
	plan := NewPlan("generation-21")
	if err := plan.Close(); err != nil {
		t.Fatal(err)
	}
	applied := false
	if _, err := plan.Acquire("late-route", func() error {
		applied = true
		return nil
	}, func() error { return nil }); err == nil {
		t.Fatal("Acquire succeeded after Close")
	}
	if applied {
		t.Fatal("Acquire applied a route after Close")
	}
}

func TestLeaseCloseIsSafeForConcurrentRollbackAndNormalShutdown(t *testing.T) {
	plan := NewPlan("generation-22")
	var mu sync.Mutex
	releases := 0
	lease, err := plan.Acquire("session-route", func() error { return nil }, func() error {
		mu.Lock()
		releases++
		mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	var wait sync.WaitGroup
	for range 12 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			if err := lease.Close(); err != nil {
				t.Errorf("Lease.Close() error = %v", err)
			}
		}()
	}
	close(start)
	wait.Wait()
	if err := plan.Close(); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if releases != 1 {
		t.Fatalf("release executions = %d, want 1", releases)
	}
}
