//go:build linux && !(android || ios)

package runtime

import (
	"errors"
	"testing"
)

func TestRecoverInterruptedStateUsesProductOwnedLinuxConfiguration(t *testing.T) {
	original := recoverOwnedLinuxRoutes
	t.Cleanup(func() { recoverOwnedLinuxRoutes = original })

	var gotTable, gotPriority int
	var gotTun string
	recoverOwnedLinuxRoutes = func(tableID, priority int, tunName string) error {
		gotTable, gotPriority, gotTun = tableID, priority, tunName
		return nil
	}

	if err := RecoverInterruptedState(); err != nil {
		t.Fatal(err)
	}
	if gotTable != ownedRoutingTableID || gotPriority != ownedRoutingPriority || gotTun != ownedTunName {
		t.Fatalf("recovery configuration = (%d, %d, %q), want (%d, %d, %q)",
			gotTable, gotPriority, gotTun,
			ownedRoutingTableID, ownedRoutingPriority, ownedTunName,
		)
	}
}

func TestRecoverInterruptedStateFailsClosedOnCleanupError(t *testing.T) {
	original := recoverOwnedLinuxRoutes
	t.Cleanup(func() { recoverOwnedLinuxRoutes = original })
	errSyntheticRecoveryFailure := errors.New("synthetic recovery failure")
	recoverOwnedLinuxRoutes = func(int, int, string) error {
		return errSyntheticRecoveryFailure
	}

	if err := RecoverInterruptedState(); err == nil {
		t.Fatal("recovery unexpectedly succeeded")
	}
}
