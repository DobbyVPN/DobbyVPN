//go:build (linux && !android) || windows

package runtime

import (
	"context"
	"core/dnscache"
	"core/sessionapi"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"syscall"
)

type protectedRestoreLoader struct{}

func ProtectedRestoreLoader() sessionapi.ConfigLoader { return protectedRestoreLoader{} }

// Use a fresh transport so pooled connections cannot outlive this restore.
// The normal configuration loader still owns HTTPS, redirects, certificate
// validation, the body limit and classification of fetch failures.
func (protectedRestoreLoader) Load(ctx context.Context, source []byte) (config sessionapi.LoadedConfig, err error) {
	dial, release, err := openRestoreDialer(ctx)
	if err != nil {
		return config, err
	}
	defer func() {
		if closeErr := release(); closeErr != nil {
			config = sessionapi.LoadedConfig{}
			err = errors.Join(err, closeErr)
		}
	}()
	transport := &http.Transport{DialContext: dial, DisableKeepAlives: true, ForceAttemptHTTP2: true}
	defer transport.CloseIdleConnections()
	return (sessionapi.DefaultConfigLoader{Client: &http.Client{Transport: transport}}).Load(ctx, source)
}

// Preserve the normal dial-error classification when route discovery itself
// establishes there is no physical network (before a socket can be opened).
func restoreRouteError(network string, err error) error {
	if errors.Is(err, syscall.ENETUNREACH) {
		return &net.OpError{Op: "dial", Net: network, Err: err}
	}
	return err
}

type restoreDial func(context.Context, string, string) (net.Conn, error)

// The HTTP client owns the original TLS hostname. A literal loopback origin
// stays local, and all other network connections use a numeric physical peer.
func dialRestoreOrigin(ctx context.Context, network, address string, cache *dnscache.Cache, dial restoreDial) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	if isRestoreLoopback(host) {
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}
	addresses, err := cache.ResolveAddresses(ctx, host)
	if err != nil {
		return nil, err
	}
	var failures []error
	for _, ip := range addresses {
		conn, err := dial(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		failures = append(failures, err)
		if ctx.Err() != nil {
			break
		}
	}
	return nil, errors.Join(failures...)
}

func isRestoreLoopback(host string) bool {
	ip, err := netip.ParseAddr(host)
	return (err == nil && ip.IsLoopback()) || strings.EqualFold(host, "localhost")
}
