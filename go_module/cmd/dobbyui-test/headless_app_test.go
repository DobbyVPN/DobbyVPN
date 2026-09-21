//go:build !(android || ios)

package main

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
)

func TestSerializedTestDriverExecutesOneCallbackAtATime(t *testing.T) {
	driver := newSerializedTestDriver(test.NewDriver())
	defer driver.close()

	var active atomic.Int32
	var maximum atomic.Int32
	var completed atomic.Int32
	var wait sync.WaitGroup
	for index := 0; index < 32; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			driver.DoFromGoroutine(func() {
				current := active.Add(1)
				for {
					old := maximum.Load()
					if current <= old || maximum.CompareAndSwap(old, current) {
						break
					}
				}
				time.Sleep(time.Microsecond)
				active.Add(-1)
				completed.Add(1)
			}, true)
		}()
	}
	wait.Wait()

	if maximum.Load() != 1 {
		t.Fatalf("callbacks overlapped: maximum active callbacks = %d", maximum.Load())
	}
	if completed.Load() != 32 {
		t.Fatalf("completed callbacks = %d, want 32", completed.Load())
	}
}

func TestSerializedTestDriverAllowsNestedAsyncWork(t *testing.T) {
	driver := newSerializedTestDriver(test.NewDriver())
	defer driver.close()

	var nested atomic.Bool
	driver.DoFromGoroutine(func() {
		driver.DoFromGoroutine(func() { nested.Store(true) }, false)
	}, true)
	driver.DoFromGoroutine(func() {
		if !nested.Load() {
			t.Error("nested asynchronous callback did not run in FIFO order")
		}
	}, true)
}

func TestSerializedTestDriverCloseDrainsQueuedWork(t *testing.T) {
	driver := newSerializedTestDriver(test.NewDriver())
	var completed atomic.Bool
	driver.DoFromGoroutine(func() { completed.Store(true) }, false)
	driver.close()
	driver.close()
	if !completed.Load() {
		t.Fatal("driver closed before queued work completed")
	}
}

func TestSerializedTestAppQuitFromCallbackDoesNotSelfJoin(t *testing.T) {
	application := newSerializedTestApp()
	defer application.close()

	done := make(chan struct{})
	go func() {
		application.driver.DoFromGoroutine(application.Quit, true)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Quit called from a UI callback did not return")
	}
}
