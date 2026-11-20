//go:build darwin && !(android || ios)

package routing

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"core/log"
	"core/tunnel/protected_dialer"
)

// The token lives in /var/run, which macOS clears at boot together with the
// in-kernel pf state, so a reboot leaves nothing behind.
var macOSPFTokenPath = "/var/run/dobbyvpn/pf.token"

var macOSPFCommand = func(ctx context.Context, stdin string, args ...string) (string, string, error) {
	command := exec.CommandContext(ctx, "/sbin/pfctl", args...)
	command.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	errorText := ""
	if err != nil {
		errorText = err.Error()
	}
	log.Debug(Category, "macOS pf command completed", map[string]any{
		"arguments": args, "stdout": stdout.String(), "stderr": stderr.String(), "error": errorText,
	})
	if err != nil {
		err = fmt.Errorf("pfctl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), stderr.String(), err
}

// Unlike the Linux and Windows guards, pf rules outlive the backend process:
// if the backend dies while connected, traffic stays blocked. One kill switch
// owns the anchor at a time. The next owner adopts rules and the enable token
// a dead backend left behind; ReleaseUnadoptedMacOSKillSwitch removes them
// when no owner takes over.
var macOSPFOwner struct {
	sync.Mutex
	active bool
}

type MacOSKillSwitch struct {
	mu             sync.Mutex
	tunName        string
	token          string
	servers        []netip.Addr
	excluded       []netip.Prefix
	attempts       map[uint64]map[PFDestination]struct{}
	retiredThrough uint64
	applied        bool
	closed         bool
	// stale is set when the loaded rules may differ from the state above, so
	// the next change reloads even if it changes nothing itself.
	stale bool
}

// AcquireMacOSKillSwitch blocks outbound traffic outside tunName with rules in
// MacOSPFAnchor. A pf configuration without the stock com.apple anchor is
// refused without changing the system ruleset; an unprotected connection is not accepted.
func (p *Plan) AcquireMacOSKillSwitch(ctx context.Context, tunName string, servers []netip.Addr) (*MacOSKillSwitch, error) {
	k := &MacOSKillSwitch{tunName: tunName, servers: servers, attempts: make(map[uint64]map[PFDestination]struct{})}
	mainRules, _, err := macOSPFCommand(ctx, "", "-s", "rules")
	if err != nil {
		return nil, err
	}
	if !PFEvaluatesAnchor(mainRules) {
		log.Error(Category, `macOS kill switch not applied: pf rules were not loaded because /etc/pf.conf has no anchor "com.apple/*"`, map[string]any{"anchor": MacOSPFAnchor})
		return nil, errors.New(`macOS VPN protection requires the com.apple/* pf anchor; no connection was started`)
	}
	macOSPFOwner.Lock()
	defer macOSPFOwner.Unlock()
	if macOSPFOwner.active {
		return nil, errors.New("macOS kill switch is owned by another network")
	}
	if _, err := p.Acquire("macOS pf kill switch", func() error { return nil }, k.Close); err != nil {
		return nil, err
	}
	macOSPFOwner.active, k.applied = true, true
	k.token, err = macOSPFEnable(ctx)
	if err == nil {
		err = k.reload(ctx)
	}
	if err == nil {
		err = killMacOSPFStates(ctx)
	}
	if err == nil {
		log.Info(Category, "macOS kill switch applied", map[string]any{"anchor": MacOSPFAnchor, "interface": tunName})
	}
	return k, err
}

// A block rule does not stop flows that already have pf state, which exists
// only if another owner enabled pf with its own pass rules. Kill every state
// once the anchor is loaded, so existing direct connections must pass the new
// rules again. This runs before the network is ready, when no VPN transport
// connection exists yet; later reloads never kill states. Loopback and tunnel
// rules keep no state, so their flows continue.
func killMacOSPFStates(ctx context.Context) error {
	for _, network := range []string{"0.0.0.0/0", "::/0"} {
		if _, _, err := macOSPFCommand(ctx, "", "-k", network); err != nil {
			return err
		}
	}
	return nil
}

// macOSPFEnable adds a pf enable reference, or adopts the one a dead backend
// left. A pf that was already enabled stays enabled after release.
func macOSPFEnable(ctx context.Context) (string, error) {
	if saved, err := os.ReadFile(macOSPFTokenPath); err == nil {
		if token, parseErr := PFEnableToken("Token : " + strings.TrimSpace(string(saved))); parseErr == nil {
			references, _, inspectErr := macOSPFCommand(ctx, "", "-s", "References")
			if inspectErr != nil {
				return "", inspectErr
			}
			held, inspectErr := pfHasReference(references, token)
			if inspectErr != nil {
				return "", inspectErr
			}
			if held {
				log.Info(Category, "macOS kill switch adopts rules left by an interrupted backend", nil)
				return token, nil
			}
			log.Warn(Category, "saved macOS pf reference is no longer held; acquiring a new reference", map[string]any{"token": token})
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	stdout, stderr, err := macOSPFCommand(ctx, "", "-E")
	if err != nil {
		return "", err
	}
	token, err := PFEnableToken(stderr + "\n" + stdout)
	if err != nil {
		return "", err
	}
	// The saved token is the only record a later backend can release; rules
	// are loaded only after it is written.
	err = os.MkdirAll(filepath.Dir(macOSPFTokenPath), 0o755)
	if err == nil {
		err = os.WriteFile(macOSPFTokenPath, []byte(token+"\n"), 0o600)
	}
	if err != nil {
		_, _, releaseErr := macOSPFCommand(context.WithoutCancel(ctx), "", "-X", token)
		if releaseErr != nil {
			// Keep ownership in memory so the registered cleanup can still
			// release the reference even though crash persistence failed.
			return token, errors.Join(err, releaseErr)
		}
		if removeErr := os.Remove(macOSPFTokenPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			err = errors.Join(err, removeErr)
		}
		return "", err
	}
	return token, nil
}

func releaseMacOSPF(ctx context.Context, token string) error {
	for _, kind := range []string{"rules", "Tables"} {
		if _, _, err := macOSPFCommand(ctx, "", "-a", MacOSPFAnchor, "-F", kind); err != nil {
			return err
		}
	}
	// Only a saved token has a file. It stays until pf accepts the release, so
	// a retry, the next service start or the uninstall script can repeat it.
	if token == "" {
		return nil
	}
	// A token pf no longer holds (released before its file was removed)
	// cannot be released again; drop its file instead of failing forever.
	references, _, err := macOSPFCommand(ctx, "", "-s", "References")
	if err != nil {
		return err
	}
	held, err := pfHasReference(references, token)
	if err != nil {
		return err
	}
	if held {
		if _, _, err := macOSPFCommand(ctx, "", "-X", token); err != nil {
			return err
		}
	}
	if err := os.Remove(macOSPFTokenPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// ReleaseUnadoptedMacOSKillSwitch removes rules a dead backend left when no
// network adopted them. The service calls it after its startup restore ends.
func ReleaseUnadoptedMacOSKillSwitch(ctx context.Context) error {
	macOSPFOwner.Lock()
	defer macOSPFOwner.Unlock()
	if macOSPFOwner.active {
		return nil
	}
	saved, err := os.ReadFile(macOSPFTokenPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	log.Info(Category, "removing macOS kill switch left by an interrupted backend", map[string]any{"anchor": MacOSPFAnchor})
	return releaseMacOSPF(ctx, strings.TrimSpace(string(saved)))
}

func (k *MacOSKillSwitch) reload(ctx context.Context) error {
	var destinations []PFDestination
	for _, entries := range k.attempts {
		for destination := range entries {
			destinations = append(destinations, destination)
		}
	}
	rules := MacOSPFRules(k.tunName, k.servers, k.excluded, destinations)
	if _, _, err := macOSPFCommand(ctx, rules, "-a", MacOSPFAnchor, "-f", "-"); err != nil {
		k.stale = true
		return err
	}
	k.stale = false
	log.Debugf(Category, "macOS kill switch rules loaded:\n%s", rules)
	return nil
}

// update applies change and loads the result in one step under k.mu. A load
// failure marks the rules stale, so a retried change reloads them.
func (k *MacOSKillSwitch) update(ctx context.Context, change func() (bool, error)) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.closed {
		return errors.New("macOS kill switch is closed")
	}
	changed, err := change()
	if err != nil {
		return err
	}
	if !k.applied || !changed && !k.stale {
		return nil
	}
	return k.reload(ctx)
}

// SetResolvers admits the backend's queries to the physical DNS servers.
func (k *MacOSKillSwitch) SetResolvers(ctx context.Context, servers []netip.Addr) error {
	return k.update(ctx, func() (bool, error) {
		if slices.Equal(k.servers, servers) {
			return false, nil
		}
		k.servers = slices.Clone(servers)
		return true, nil
	})
}

// SetExcluded replaces the excluded table (ExcludeIPs) as a whole.
func (k *MacOSKillSwitch) SetExcluded(ctx context.Context, excluded []*net.IPNet) error {
	merged := MergePFPrefixes(excluded)
	return k.update(ctx, func() (bool, error) {
		if slices.Equal(k.excluded, merged) {
			return false, nil
		}
		k.excluded = merged
		return true, nil
	})
}

func (k *MacOSKillSwitch) Authorize(ctx context.Context, generation uint64, network, address string) error {
	destination, err := pfTransportDestination(network, address)
	if err != nil {
		return err
	}
	destination.Bypass = protected_dialer.IsApplicationBypass(ctx)
	// The physical resolver exception belongs to bootstrap. Application DNS
	// must not reach it even though both sockets are owned by the root backend.
	if destination.Bypass && destination.Port == 53 {
		return errors.New("application DNS outside the VPN is blocked")
	}
	return k.update(ctx, func() (bool, error) {
		if generation == 0 || generation <= k.retiredThrough {
			return false, errors.New("macOS transport owner is retired")
		}
		// The excluded table already admits direct traffic to these networks.
		// Other direct destinations, such as the server's own address, keep a
		// per-connection rule.
		if destination.Bypass && slices.ContainsFunc(k.excluded, func(p netip.Prefix) bool { return p.Contains(destination.Addr) }) {
			return false, nil
		}
		entries := k.attempts[generation]
		if _, ok := entries[destination]; ok {
			return false, nil
		}
		if entries == nil {
			entries = make(map[PFDestination]struct{})
			k.attempts[generation] = entries
		}
		entries[destination] = struct{}{}
		return true, nil
	})
}

func (k *MacOSKillSwitch) Retire(ctx context.Context, generation uint64) error {
	k.mu.Lock()
	closed := k.closed
	k.mu.Unlock()
	if closed {
		return nil
	}
	return k.update(ctx, func() (bool, error) {
		if generation > k.retiredThrough {
			k.retiredThrough = generation
		}
		_, ok := k.attempts[generation]
		delete(k.attempts, generation)
		return ok, nil
	})
}

func (k *MacOSKillSwitch) Close(ctx context.Context) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.closed {
		return nil
	}
	if k.applied {
		macOSPFOwner.Lock()
		err := releaseMacOSPF(ctx, k.token)
		if err == nil {
			macOSPFOwner.active = false
		}
		macOSPFOwner.Unlock()
		if err != nil {
			return err
		}
		log.Info(Category, "macOS kill switch removed", map[string]any{"anchor": MacOSPFAnchor})
	}
	k.closed = true
	return nil
}

func pfTransportDestination(network, address string) (PFDestination, error) {
	var protocol string
	switch network {
	case "tcp4", "tcp6":
		protocol = "tcp"
	case "udp4", "udp6":
		protocol = "udp"
	default:
		return PFDestination{}, fmt.Errorf("unsupported transport network %q", network)
	}
	host, portText, err := net.SplitHostPort(address)
	var port uint64
	if err == nil {
		port, err = strconv.ParseUint(portText, 10, 16)
		if err != nil || port == 0 {
			return PFDestination{}, fmt.Errorf("invalid numeric transport port %q", portText)
		}
	} else {
		host = address
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || ip.IsUnspecified() {
		return PFDestination{}, fmt.Errorf("transport destination is not a numeric address: %q", address)
	}
	return PFDestination{Protocol: protocol, Addr: ip.Unmap().WithZone(""), Port: uint16(port)}, nil
}
