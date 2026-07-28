package controlplane

import (
	"context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"testing"
)

type testPeer bool

func (p testPeer) AuthType() string               { return "test" }
func (p testPeer) ControlPeerAuthenticated() bool { return bool(p) }

func TestUnauthorizedRequestsNeverReachHandler(t *testing.T) {
	interceptor := UnaryAuthInterceptor(true, "expected")
	called := false
	_, err := interceptor(context.Background(), nil, &grpc.UnaryServerInfo{}, func(context.Context, interface{}) (interface{}, error) { called = true; return nil, nil })
	if status.Code(err) != codes.Unauthenticated || called {
		t.Fatalf("unauthorized request reached handler: %v", err)
	}
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(TokenMetadata, "expected"))
	_, err = interceptor(ctx, nil, &grpc.UnaryServerInfo{}, func(context.Context, interface{}) (interface{}, error) { called = true; return nil, nil })
	if err != nil || !called {
		t.Fatalf("authorized request rejected: %v", err)
	}
}

func TestUnixPeerProofIsRequired(t *testing.T) {
	interceptor := UnaryAuthInterceptor(false, "")
	ctx := peer.NewContext(context.Background(), &peer.Peer{AuthInfo: testPeer(false)})
	_, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{}, func(context.Context, interface{}) (interface{}, error) { return nil, nil })
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("unverified peer accepted: %v", err)
	}
	ctx = peer.NewContext(context.Background(), &peer.Peer{AuthInfo: testPeer(true)})
	if _, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{}, func(context.Context, interface{}) (interface{}, error) { return nil, nil }); err != nil {
		t.Fatal(err)
	}
}
