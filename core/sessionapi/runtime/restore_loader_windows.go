//go:build windows && (amd64 || arm64)

package runtime

import (
	"context"
	"core/dnscache"
	"core/routing"
	"core/tunnel/protected_dialer"
	"errors"
	"net"
	"syscall"

	"golang.org/x/sys/windows"
)

func openRestoreDialer(ctx context.Context) (restoreDial, func() error, error) {
	guard, release, err := routing.ServiceWindowsProtection.OpenRestoreGuard(ctx)
	if err != nil {
		return nil, nil, err
	}
	cache := dnscache.New()
	cache.SetResolverSource(func(ctx context.Context) (string, dnscache.Resolver, error) {
		v4, v6, err := routing.WindowsPhysicalInterfaces()
		if errors.Is(err, windows.WSAENETUNREACH) {
			return "", nil, &net.OpError{Op: "dial", Net: "tcp", Err: err}
		}
		if err != nil {
			return "", nil, err
		}
		index := v4
		if index == 0 {
			index = v6
		}
		identity, servers, lookup, err := dnscache.CaptureWindowsInterface(ctx, index, v4 != 0, v6 != 0)
		if err != nil {
			return "", nil, err
		}
		if err := guard.UpdateBootstrap(ctx, identity, servers); err != nil {
			return "", nil, err
		}
		return identity, lookup, nil
	})
	dialer := net.Dialer{ControlContext: func(ctx context.Context, network, address string, socket syscall.RawConn) error {
		if err := guard.Authorize(ctx, 1, network, address); err != nil {
			return err
		}
		var bindErr error
		err := socket.Control(func(fd uintptr) { bindErr = protected_dialer.BindWindowsPhysicalSocket(fd, network, address) })
		return errors.Join(err, bindErr)
	}}
	dial := func(ctx context.Context, network, address string) (net.Conn, error) {
		return dialRestoreOrigin(ctx, network, address, cache, dialer.DialContext)
	}
	return dial, release, nil
}
