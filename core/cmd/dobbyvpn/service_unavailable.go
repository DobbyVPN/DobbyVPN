package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"runtime"
	"syscall"

	"core/clientserver/controljson"
)

// failureServiceUnavailable is the machine-readable code written to stdout when
// the CLI cannot reach the Dobby VPN service at all. It is local to the CLI:
// the service never sends it.
const failureServiceUnavailable = "SERVICE_UNAVAILABLE"

// serviceUnavailableError reports that the CLI could not open the service's
// control endpoint. It is raised only for connection setup, so a service that
// answered and then failed is still an ordinary operation failure.
type serviceUnavailableError struct{ cause error }

func (e *serviceUnavailableError) Error() string {
	reason := "the Dobby VPN service is not running or not reachable"
	hint := serviceHint(runtime.GOOS)
	if errors.Is(e.cause, os.ErrPermission) {
		reason = "access to the Dobby VPN service was denied"
		hint = "run dobby-cli as the user Dobby VPN was installed for, without sudo or another account"
	}
	return fmt.Sprintf("%s (%v); %s", reason, e.cause, hint)
}

func (e *serviceUnavailableError) Unwrap() error { return e.cause }

func serviceHint(goos string) string {
	switch goos {
	case "linux":
		return "check it with: systemctl status dobbyvpn"
	case darwinPlatform:
		return "check that Dobby VPN is installed and its launchd job com.dobby.vpnservice is loaded"
	case windowsPlatform:
		return `check that the "DobbyVPN Go backend" service is running (services.msc)`
	default:
		return "check that the Dobby VPN service is running"
	}
}

// serviceAbsent reports whether a dial failure means that nobody serves the
// control endpoint for this user: the socket or pipe is missing, the connection
// is refused, or access is denied. Timeouts, a busy pipe and other failures say
// nothing about the service being absent and stay ordinary operation errors.
func serviceAbsent(err error) bool {
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.ECONNREFUSED)
}

// withUnavailableDial marks a dial failure caused by an absent service as
// serviceUnavailableError. The client's Dial must be set.
func withUnavailableDial(client controljson.Client) controljson.Client {
	dial := client.Dial
	client.Dial = func(ctx context.Context) (net.Conn, error) {
		conn, err := dial(ctx)
		if err != nil && serviceAbsent(err) {
			return nil, &serviceUnavailableError{cause: err}
		}
		return conn, err
	}
	return client
}
