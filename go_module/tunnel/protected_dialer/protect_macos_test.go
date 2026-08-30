//go:build darwin && !(android || ios)

package protected_dialer

import "testing"

func TestResetDefaultRouteClearsGenerationBinding(t *testing.T) {
	defaultInterfaceIndex = 42
	t.Cleanup(func() { defaultInterfaceIndex = 0 })

	ResetDefaultRoute()
	if defaultInterfaceIndex != 0 {
		t.Fatalf("default interface index = %d, want 0", defaultInterfaceIndex)
	}
}
