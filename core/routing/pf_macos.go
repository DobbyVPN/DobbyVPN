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
	excluded       []*net.IPNet
	attempts       map[uint64]map[PFDestination]struct{}
	retiredThrough uint64
	applied        bool
	closed         bool
}

// AcquireMacOSKillSwitch blocks outbound traffic outside tunName with rules in
// MacOSPFAnchor. A pf configuration without the stock com.apple anchor is
// logged and left untouched; the network then runs without a kill switch.
func (p *Plan) AcquireMacOSKillSwitch(ctx context.Context, tunName string, servers []netip.Addr) (*MacOSKillSwitch, error) {
	k := &MacOSKillSwitch{tunName: tunName, servers: servers, attempts: make(map[uint64]map[PFDestination]struct{})}
	mainRules, _, err := macOSPFCommand(ctx, "", "-s", "rules")
	if err != nil {
		return nil, err
	}
	if !PFEvaluatesAnchor(mainRules) {
		log.Error(Category, `macOS kill switch not applied: pf rules were not loaded because /etc/pf.conf has no anchor "com.apple/*"`, map[string]any{"anchor": MacOSPFAnchor})
		return k, nil
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
		log.Info(Category, "macOS kill switch applied", map[string]any{"anchor": MacOSPFAnchor, "interface": tunName})
	}
	return k, err
}

// macOSPFEnable adds a pf enable reference, or adopts the one a dead backend
// left. A pf that was already enabled stays enabled after release.
func macOSPFEnable(ctx context.Context) (string, error) {
	if saved, err := os.ReadFile(macOSPFTokenPath); err == nil {
		if token, parseErr := PFEnableToken("Token : " + strings.TrimSpace(string(saved))); parseErr == nil {
			log.Info(Category, "macOS kill switch adopts rules left by an interrupted backend", nil)
			return token, nil
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
	if err := os.MkdirAll(filepath.Dir(macOSPFTokenPath), 0o755); err == nil {
		err = os.WriteFile(macOSPFTokenPath, []byte(token+"\n"), 0o600)
	}
	if err != nil {
		_, _, releaseErr := macOSPFCommand(context.WithoutCancel(ctx), "", "-X", token)
		return "", errors.Join(err, releaseErr)
	}
	return token, nil
}

func releaseMacOSPF(ctx context.Context, token string) error {
	if _, _, err := macOSPFCommand(ctx, "", "-a", MacOSPFAnchor, "-F", "rules"); err != nil {
		return err
	}
	// A token that pf no longer knows cannot be released again; keep going so
	// cleanup does not stay pending forever.
	if token != "" {
		if _, _, err := macOSPFCommand(ctx, "", "-X", token); err != nil {
			log.Error(Category, "macOS pf enable reference release failed", map[string]any{"cause": err.Error()})
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
	rules := MacOSPFRules(k.tunName, k.servers, destinations, k.excluded)
	if _, _, err := macOSPFCommand(ctx, rules, "-a", MacOSPFAnchor, "-f", "-"); err != nil {
		return err
	}
	log.Debugf(Category, "macOS kill switch rules loaded:\n%s", rules)
	return nil
}

func (k *MacOSKillSwitch) update(ctx context.Context, change func() bool) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.closed {
		return errors.New("macOS kill switch is closed")
	}
	if !change() || !k.applied {
		return nil
	}
	return k.reload(ctx)
}

// SetResolvers admits the backend's queries to the physical DNS servers.
func (k *MacOSKillSwitch) SetResolvers(ctx context.Context, servers []netip.Addr) error {
	return k.update(ctx, func() bool {
		if slices.Equal(k.servers, servers) {
			return false
		}
		k.servers = slices.Clone(servers)
		return true
	})
}

func (k *MacOSKillSwitch) SetExcluded(ctx context.Context, excluded []*net.IPNet) error {
	return k.update(ctx, func() bool { k.excluded = excluded; return true })
}

func (k *MacOSKillSwitch) Authorize(ctx context.Context, generation uint64, network, address string) error {
	destination, err := pfTransportDestination(network, address)
	if err != nil {
		return err
	}
	k.mu.Lock()
	retired := generation == 0 || generation <= k.retiredThrough
	k.mu.Unlock()
	if retired {
		return errors.New("macOS transport owner is retired")
	}
	return k.update(ctx, func() bool {
		entries := k.attempts[generation]
		if _, ok := entries[destination]; ok {
			return false
		}
		if entries == nil {
			entries = make(map[PFDestination]struct{})
			k.attempts[generation] = entries
		}
		entries[destination] = struct{}{}
		return true
	})
}

func (k *MacOSKillSwitch) Retire(ctx context.Context, generation uint64) error {
	k.mu.Lock()
	if generation > k.retiredThrough {
		k.retiredThrough = generation
	}
	closed := k.closed
	k.mu.Unlock()
	if closed {
		return nil
	}
	return k.update(ctx, func() bool {
		_, ok := k.attempts[generation]
		delete(k.attempts, generation)
		return ok
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
