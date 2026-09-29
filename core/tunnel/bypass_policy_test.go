package tunnel

import (
	"context"
	"errors"
	"net/netip"
	"testing"

	M "github.com/xjasonlyu/tun2socks/v2/metadata"
)

func TestBypassPoliciesAreIsolatedPerAttempt(t *testing.T) {
	first, err := ResolveBypassPolicy(context.Background(), []string{"198.51.100.0/24"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := ResolveBypassPolicy(context.Background(), []string{"192.0.2.0/24"})
	if err != nil {
		t.Fatal(err)
	}

	firstDestination := &M.Metadata{DstIP: netip.MustParseAddr("198.51.100.7")}
	secondDestination := &M.Metadata{DstIP: netip.MustParseAddr("192.0.2.7")}
	if !first.IsBypass(firstDestination) || second.IsBypass(firstDestination) {
		t.Fatal("first policy did not remain isolated to its own CIDR")
	}
	if !second.IsBypass(secondDestination) || first.IsBypass(secondDestination) {
		t.Fatal("second policy did not remain isolated to its own CIDR")
	}
}

func TestBypassPolicyCopiesCallerInput(t *testing.T) {
	entries := []string{"198.51.100.0/24"}
	policy, err := ResolveBypassPolicy(context.Background(), entries)
	if err != nil {
		t.Fatal(err)
	}
	entries[0] = "203.0.113.0/24"
	if !policy.IsBypass(&M.Metadata{DstIP: netip.MustParseAddr("198.51.100.7")}) {
		t.Fatal("policy changed with caller input")
	}
}

func TestCanceledBypassResolutionLeavesExistingPolicyUsable(t *testing.T) {
	policy, err := ResolveBypassPolicy(context.Background(), []string{"192.0.2.0/24"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ResolveBypassPolicy(ctx, []string{"vpn.example.invalid"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled resolve error=%v", err)
	}
	if !policy.IsBypass(&M.Metadata{DstIP: netip.MustParseAddr("192.0.2.7")}) {
		t.Fatal("canceled resolution changed the existing attempt policy")
	}
}
