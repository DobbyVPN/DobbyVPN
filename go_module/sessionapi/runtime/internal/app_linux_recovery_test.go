//go:build linux && !(android || ios)

package internal

import (
	"fmt"
	"testing"
)

func TestReconcileLinuxUplinkPublishesRediscoveredRouteAfterSuccess(t *testing.T) {
	originalDiscover := discoverLinuxUplink
	originalReconcile := reconcileLinuxRoutes
	t.Cleanup(func() {
		discoverLinuxUplink = originalDiscover
		reconcileLinuxRoutes = originalReconcile
	})
	discoverLinuxUplink = func() (string, string, error) { return "192.0.2.9", "eth7", nil }
	var gotServer, gotGateway, gotIface string
	var gotTable, gotPriority int
	reconcileLinuxRoutes = func(server, gateway, iface string, table, priority int) error {
		gotServer, gotGateway, gotIface = server, gateway, iface
		gotTable, gotPriority = table, priority
		return nil
	}
	if err := reconcileLinuxUplink("198.51.100.9", 233, 23333); err != nil {
		t.Fatal(err)
	}
	if gotServer != "198.51.100.9" || gotGateway != "192.0.2.9" || gotIface != "eth7" || gotTable != 233 || gotPriority != 23333 {
		t.Fatalf("reconcile args = %s/%s/%s table=%d priority=%d", gotServer, gotGateway, gotIface, gotTable, gotPriority)
	}
}

func TestReconcileLinuxUplinkDoesNotPublishFailedRoute(t *testing.T) {
	originalDiscover := discoverLinuxUplink
	originalReconcile := reconcileLinuxRoutes
	t.Cleanup(func() {
		discoverLinuxUplink = originalDiscover
		reconcileLinuxRoutes = originalReconcile
	})
	discoverLinuxUplink = func() (string, string, error) { return "192.0.2.9", "eth7", nil }
	reconcileLinuxRoutes = func(string, string, string, int, int) error { return fmt.Errorf("route restore failed") }
	if err := reconcileLinuxUplink("198.51.100.9", 233, 23333); err == nil {
		t.Fatal("reconcile unexpectedly succeeded")
	}
}
