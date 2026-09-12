package runtime

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

func TestResourceLedgerRollsBackInLIFOOrderOnce(t *testing.T) {
	var order []string
	ledger := &resourceLedger{}
	ledger.Add(func() error { order = append(order, "tun"); return nil })
	ledger.Add(func() error { order = append(order, "protocol"); return nil })
	ledger.Add(func() error { order = append(order, "engine"); return nil })

	if err := ledger.Rollback(); err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}
	if want := []string{"engine", "protocol", "tun"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("cleanup order = %v, want %v", order, want)
	}
	if err := ledger.Rollback(); err != nil {
		t.Fatalf("second Rollback() error = %v", err)
	}
	if got := len(order); got != 3 {
		t.Fatalf("cleanup ran %d times, want 3", got)
	}
}

func TestResourceLedgerReleaseDisarmsTransferredResource(t *testing.T) {
	called := false
	ledger := &resourceLedger{}
	release := ledger.Add(func() error { called = true; return errors.New("must not run") })
	release()
	if err := ledger.Rollback(); err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}
	if called {
		t.Fatal("released resource was rolled back")
	}
}

func TestResourceLedgerFailureAtEveryAcquisitionRollsBackOnlyPriorResources(t *testing.T) {
	resources := []string{"engine", "protocol", "tun", "fd", "routes", "dns", "firewall", "workers"}

	for failAt := range resources {
		failAt := failAt
		t.Run(resources[failAt], func(t *testing.T) {
			ledger := &resourceLedger{}
			var cleaned []string
			for index, name := range resources {
				if index == failAt {
					break
				}
				name := name
				ledger.Add(func() error {
					cleaned = append(cleaned, name)
					return nil
				})
			}

			if err := ledger.Rollback(); err != nil {
				t.Fatalf("Rollback() error = %v", err)
			}
			want := append([]string(nil), resources[:failAt]...)
			for left, right := 0, len(want)-1; left < right; left, right = left+1, right-1 {
				want[left], want[right] = want[right], want[left]
			}
			if !reflect.DeepEqual(cleaned, want) {
				t.Fatalf("cleanup after %s acquisition failure = %v, want %v", resources[failAt], cleaned, want)
			}
		})
	}
}

func TestResourceLedgerRollbackIsSafeForConcurrentFailureAndStopPaths(t *testing.T) {
	ledger := &resourceLedger{}
	var mu sync.Mutex
	cleanups := 0
	ledger.Add(func() error {
		mu.Lock()
		cleanups++
		mu.Unlock()
		return nil
	})

	start := make(chan struct{})
	var wait sync.WaitGroup
	for range 16 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			if err := ledger.Rollback(); err != nil {
				t.Errorf("Rollback() error = %v", err)
			}
		}()
	}
	close(start)
	wait.Wait()

	mu.Lock()
	defer mu.Unlock()
	if cleanups != 1 {
		t.Fatalf("cleanup executions = %d, want 1", cleanups)
	}
}

func TestResourceLedgerAddAfterRollbackIsDisarmed(t *testing.T) {
	ledger := &resourceLedger{}
	if err := ledger.Rollback(); err != nil {
		t.Fatal(err)
	}
	called := false
	ledger.Add(func() error {
		called = true
		return nil
	})
	if err := ledger.Rollback(); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("cleanup added after rollback ran")
	}
}
