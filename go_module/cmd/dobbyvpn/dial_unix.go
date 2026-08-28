//go:build !windows

package main

import (
	"context"
	"net"

	"go_module/desktop_exports/controlplane"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func dialService() (*grpc.ClientConn, error) {
	path, err := controlplane.ControlSocketPath()
	if err != nil {
		return nil, err
	}
	return grpc.NewClient(
		"unix://"+path,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", path)
		}),
	)
}
