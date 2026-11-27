//go:build darwin && !ios

package macosprovider

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"core/log"
	"core/routing"
	"core/sessionapi"
)

const hostPath = "/Applications/Dobby VPN.app/Contents/MacOS/DobbyVPNMacApp"

// launchctl selects the installed account's GUI session for normal system
// consent. sudo drops the daemon's uid before running the signed host.
func host(ctx context.Context, args ...string) (string, error) {
	uid := os.Getenv("DOBBYVPN_CONTROL_PEER_UID")
	number, err := strconv.ParseUint(uid, 10, 32)
	if err != nil || number == 0 {
		return "", errors.New("macOS VPN installation owner unavailable")
	}
	cmd := exec.CommandContext(ctx, "/bin/launchctl", append([]string{"asuser", uid, "/usr/bin/sudo", "-n", "-H", "-u", "#" + uid, hostPath, "--network-extension"}, args...)...)
	// launchctl and sudo run the signed preference helper as the GUI user.
	// Stop must retire the whole operation before it reconciles preferences.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	output, err := cmd.Output()
	if exit, ok := err.(*exec.ExitError); ok {
		output = append(output, exit.Stderr...)
	}
	if err != nil {
		return "", fmt.Errorf("macOS VPN %s: %w: %s", args[0], err, output)
	}
	return strings.TrimSpace(string(output)), nil
}

type Desktop struct {
	prefs   sync.Mutex
	mu      sync.Mutex
	desired bool
	pending context.CancelFunc
	active  *desktopNetwork
}

func (d *Desktop) Recover(ctx context.Context) (bool, error) {
	// The signed host reads preferences without activating an extension.
	value, err := host(ctx, "status")
	if err != nil {
		return false, err
	}
	if value != "armed" && value != "idle" {
		return false, fmt.Errorf("unknown Mac protection state %q", value)
	}
	armed := value == "armed"
	if !armed {
		armed, err = routing.LegacyMacOSGuardActive(ctx)
		if err != nil {
			return false, err
		}
	}
	d.mu.Lock()
	d.desired = armed
	d.mu.Unlock()
	return armed, nil
}

func (d *Desktop) RollbackFirstStart(ctx context.Context) error { return d.SetEnabled(ctx, false) }

func (d *Desktop) PrepareRestore(ctx context.Context) error {
	d.prefs.Lock()
	defer d.prefs.Unlock()
	if _, err := host(ctx, "arm"); err != nil {
		return err
	}
	return d.SetEnabled(ctx, true)
}

func (d *Desktop) SetEnabled(ctx context.Context, enabled bool) error {
	d.mu.Lock()
	d.desired = enabled
	if !enabled && d.pending != nil {
		d.pending()
	}
	d.mu.Unlock()
	if enabled {
		return nil
	}
	d.prefs.Lock()
	defer d.prefs.Unlock()
	_, err := host(ctx, "disarm")
	return errors.Join(err, routing.ReleaseLegacyMacOSGuard(ctx))
}

// Keep an interrupted older installation's protection until its successor
// starts or the user releases it, including across a failed restore.
func (d *Desktop) ReleaseIdleLegacyGuard(ctx context.Context) error {
	d.prefs.Lock()
	defer d.prefs.Unlock()
	d.mu.Lock()
	desired := d.desired
	d.mu.Unlock()
	if desired {
		return nil
	}
	return routing.ReleaseLegacyMacOSGuard(ctx)
}

type desktopNetwork struct {
	owner *Desktop
	endpoint
	key string
}

func (d *Desktop) PrepareNetwork(ctx context.Context, ref sessionapi.SessionRef, _ sessionapi.RuntimeProfile) (sessionapi.RuntimeLease, error) {
	d.prefs.Lock()
	defer d.prefs.Unlock()
	d.mu.Lock()
	if !d.desired || d.active != nil {
		d.mu.Unlock()
		return nil, errors.New("Mac VPN activation is absent or already owned")
	}
	setup, cancel := context.WithCancel(ctx)
	d.pending = cancel
	var nonce [16]byte
	rand.Read(nonce[:])
	key := hex.EncodeToString(nonce[:])
	endpoint, err := newEndpoint(key)
	if err != nil {
		cancel()
		d.pending = nil
		d.mu.Unlock()
		return nil, err
	}
	n := &desktopNetwork{owner: d, key: key, endpoint: endpoint}
	d.active = n
	d.mu.Unlock()
	defer func() { cancel(); d.mu.Lock(); d.pending = nil; d.mu.Unlock() }()
	_, err = host(setup, "connect", key, ref.SessionID, strconv.FormatUint(ref.Generation, 10))
	if err == nil {
		err = routing.ReleaseLegacyMacOSGuard(setup)
	}
	return n, err
}

func (n *desktopNetwork) Stop(ctx context.Context) error {
	n.owner.prefs.Lock()
	defer n.owner.prefs.Unlock()
	n.owner.mu.Lock()
	active := n.owner.active == n
	n.owner.mu.Unlock()
	if !active {
		return nil
	}
	if _, err := host(ctx, "stop", n.key); err != nil {
		return err
	}
	n.owner.mu.Lock()
	n.owner.active = nil
	n.owner.mu.Unlock()
	return nil
}

func (n *desktopNetwork) reconnect(ctx context.Context) error {
	d := n.owner
	d.prefs.Lock()
	defer d.prefs.Unlock()
	d.mu.Lock()
	if !d.desired || d.active != n {
		d.mu.Unlock()
		return errors.New("Mac VPN reconnection superseded")
	}
	setup, cancel := context.WithCancel(ctx)
	d.pending = cancel
	d.mu.Unlock()
	defer func() { cancel(); d.mu.Lock(); d.pending = nil; d.mu.Unlock() }()
	_, err := host(setup, "reconnect", n.key)
	return err
}

func (d *Desktop) Start(ctx context.Context, ref sessionapi.SessionRef, profile sessionapi.RuntimeProfile) (sessionapi.RuntimeLease, error) {
	d.mu.Lock()
	n := d.active
	d.mu.Unlock()
	if n == nil {
		return nil, errors.New("Mac packet tunnel not prepared")
	}
	identity, err := n.call(ctx, operation{Action: "identity", Ref: ref})
	if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, os.ErrNotExist) {
		if err := n.reconnect(ctx); err != nil {
			return nil, err
		}
		identity, err = n.call(ctx, operation{Action: "identity", Ref: ref})
	}
	if err != nil {
		return nil, err
	}
	lifetime, cancel := context.WithCancel(context.Background())
	a := &attempt{endpoint: n.endpoint, instance: identity, ref: ref, lifetime: lifetime, cancel: cancel, health: make(chan error, 1)}
	_, err = n.call(ctx, operation{Action: "start", Instance: identity, Ref: ref, Summary: profile.Summary, Config: profile.Config,
		Excluded: profile.ExcludeSet.Ranges(), Hosts: profile.ExcludeHosts})
	if err != nil {
		return a, err
	}
	go a.monitor()
	return a, nil
}

type attempt struct {
	endpoint
	ref      sessionapi.SessionRef
	instance string
	lifetime context.Context
	cancel   context.CancelFunc
	health   chan error
}

func (a *attempt) operation(ctx context.Context, action, reason string) error {
	_, err := a.call(ctx, operation{Action: action, Ref: a.ref, Instance: a.instance, Reason: reason})
	return err
}

func (a *attempt) Stop(ctx context.Context) error {
	a.cancel()
	err := a.operation(ctx, "stop", "")
	if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (a *attempt) monitor() {
	defer close(a.health)
	err := a.operation(a.lifetime, "health", "")
	if a.lifetime.Err() == nil {
		if err == nil {
			err = errors.New("packet tunnel health monitor terminated")
		}
		a.health <- err
	}
}
func (a *attempt) HealthFailures() <-chan error { return a.health }
func (a *attempt) ProbeNow(reason string) {
	go func() {
		if err := a.operation(a.lifetime, "probe", reason); err != nil && a.lifetime.Err() == nil {
			log.Warnf("MacPacketProvider", "Request protocol health probe: %v", err)
		}
	}()
}
