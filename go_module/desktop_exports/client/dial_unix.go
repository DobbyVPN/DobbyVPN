//go:build !(windows || android || ios)

package client

import (
	"context"
	"net"

	"go_module/desktop_exports/controlplane"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func Dial() (*grpc.ClientConn, error) {
	path, err := controlplane.ControlSocketPath()
	if err != nil {
		return nil, err
	}
	return grpc.NewClient(
		"unix://"+path,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, "unix", path)
		}),
	)
}
