// Package controlplane contains native-free authentication primitives for the
// privileged desktop RPC boundary.
package controlplane

import (
	"context"
	"crypto/subtle"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

const TokenMetadata = "x-dobby-control-token"

type PeerAuth interface{ ControlPeerAuthenticated() bool }

func UnaryAuthInterceptor(requireToken bool, expectedToken string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		if !requireToken {
			if p, ok := peer.FromContext(ctx); ok {
				if auth, ok := p.AuthInfo.(PeerAuth); ok && auth.ControlPeerAuthenticated() {
					return handler(ctx, req)
				}
			}
			return nil, status.Error(codes.Unauthenticated, "local peer authentication required")
		}
		values := metadata.ValueFromIncomingContext(ctx, TokenMetadata)
		if len(values) != 1 || expectedToken == "" || subtle.ConstantTimeCompare([]byte(values[0]), []byte(expectedToken)) != 1 {
			return nil, status.Error(codes.Unauthenticated, "control authentication required")
		}
		return handler(ctx, req)
	}
}
