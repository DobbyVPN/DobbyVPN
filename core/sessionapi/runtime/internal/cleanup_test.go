package internal

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestCleanupRetainsFailedDependencyAndUsesRetryDeadline(t *testing.T) {
	app := &App{}
	failure := errors.New("routing still owned")
	var released []string
	attempts := 0
	var deadlines []time.Time
	app.setCleanup(func(ctx context.Context) error {
		attempts++
		deadline, _ := ctx.Deadline()
		deadlines = append(deadlines, deadline)
		if attempts == 1 {
			return failure
		}
		released = append(released, "routes")
		return nil
	}, func(context.Context) error { released = append(released, "engine"); return nil })
	first, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	if err := app.Close(first); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	cancel()
	if len(released) != 0 {
		t.Fatal("released a resource beneath failed routing cleanup")
	}
	retry, cancelRetry := context.WithTimeout(context.Background(), time.Second)
	defer cancelRetry()
	if err := app.Close(retry); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(released, []string{"routes", "engine"}) {
		t.Fatal(released)
	}
	if !deadlines[1].After(deadlines[0]) {
		t.Fatal("retry reused expired cleanup budget")
	}
	if err := app.Close(retry); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 || app.CleanupError() != nil {
		t.Fatalf("attempts=%d error=%v", attempts, app.CleanupError())
	}
}
