//go:build darwin && !(android || ios)

package routing

import (
	"context"
	"errors"
	"golang.org/x/sys/unix"
	"net/netip"
	"testing"
)

func TestMacOSRouteOwnershipAndRepair(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		t.Run(map[bool]string{true: "foreign", false: "owned"}[foreign], func(t *testing.T) {
			oldRead, oldChange := macOSReadRoutes, macOSChangeRoute
			t.Cleanup(func() { macOSReadRoutes, macOSChangeRoute = oldRead, oldChange })
			want := macOSRoute{netip.MustParsePrefix("198.51.100.8/32"), netip.MustParseAddr("192.0.2.1"), 7, unix.RTF_UP | unix.RTF_STATIC | unix.RTF_GATEWAY | unix.RTF_HOST}
			var rows []macOSRoute
			if foreign {
				rows = []macOSRoute{want}
			}
			macOSReadRoutes = func() ([]macOSRoute, error) { return rows, nil }
			creates, deletes := 0, 0
			macOSChangeRoute = func(_ context.Context, operation int, r macOSRoute) error {
				if !r.same(want) {
					t.Fatalf("changed route identity: %+v", r)
				}
				if operation == unix.RTM_ADD {
					creates++
					rows = []macOSRoute{r}
				} else {
					deletes++
					rows = nil
				}
				return nil
			}
			plan := NewPlan("test")
			if _, err := plan.acquireMacOSRoute(context.Background(), want); err != nil {
				t.Fatal(err)
			}
			if foreign {
				if err := plan.Close(); err != nil {
					t.Fatal(err)
				}
				if creates != 0 || deletes != 0 {
					t.Fatalf("foreign route mutated: create=%d delete=%d", creates, deletes)
				}
				return
			}
			rows = nil // An uplink transition removed this session's route.
			repaired, err := plan.Repair()
			if err != nil || !repaired || creates != 2 {
				t.Fatalf("repair=%v creates=%d error=%v", repaired, creates, err)
			}
			rows[0].index = 8 // Another actor has replaced the route.
			if _, err := plan.Repair(); err == nil {
				t.Fatal("repair accepted foreign replacement")
			}
			if err := plan.Close(); err == nil {
				t.Fatal("cleanup accepted foreign replacement")
			}
			if deletes != 0 {
				t.Fatal("foreign replacement deleted")
			}
			rows[0].index = 7
			if err := plan.Close(); err != nil {
				t.Fatal(err)
			}
			if deletes != 1 {
				t.Fatalf("owned deletions=%d", deletes)
			}
		})
	}
}

func TestMacOSPartialAcquisitionRetainsOriginalFailure(t *testing.T) {
	oldRead, oldChange := macOSReadRoutes, macOSChangeRoute
	t.Cleanup(func() { macOSReadRoutes, macOSChangeRoute = oldRead, oldChange })
	want := macOSRoute{prefix: netip.MustParsePrefix("0.0.0.0/1"), index: 17, flags: unix.RTF_UP | unix.RTF_STATIC}
	var rows []macOSRoute
	macOSReadRoutes = func() ([]macOSRoute, error) { return rows, nil }
	failure := errors.New("route acknowledgement unavailable")
	macOSChangeRoute = func(_ context.Context, operation int, r macOSRoute) error {
		if operation == unix.RTM_ADD {
			rows = []macOSRoute{r}
			return failure
		}
		rows = nil
		return nil
	}
	plan := NewPlan("test")
	if _, err := plan.acquireMacOSRoute(context.Background(), want); !errors.Is(err, failure) {
		t.Fatalf("error=%v", err)
	}
	if err := plan.Close(); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatal("partial setup route survived cleanup")
	}
}
