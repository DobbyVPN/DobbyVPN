package outline

import (
	"context"
	"crypto/tls"
	"strings"
	"sync"
	"time"

	"core/log"
)

const (
	// ipv6ProbeTimeout bounds one end-to-end probe through the profile.
	ipv6ProbeTimeout = 4 * time.Second
	// ipv6UnsupportedRetry is how long a "no IPv6" result is trusted before the
	// profile is probed again; the last result stays in force meanwhile.
	ipv6UnsupportedRetry = time.Minute
	// ipv6InconclusiveRetry spaces probes while the profile itself is
	// unreachable, so a stream of IPv6 connects does not keep dialing it.
	ipv6InconclusiveRetry = 15 * time.Second
	ipv6ProbeRequest      = "HEAD / HTTP/1.0\r\n\r\n"
)

// A reply from an IPv6 target proves that the server forwards IPv6; a stream
// dial alone does not, because Shadowsocks only connects to the server. The
// targets are several independent public IPv6 endpoints of different operators,
// on ports 80 and 443, so IPv6 counts as missing only when none of them answers
// while the IPv4 control does. When the control is silent too, the profile
// itself is unreachable and the probe says nothing about IPv6.
var (
	ipv6ProbeTargets = []string{
		"[2606:4700:4700::1111]:80", "[2606:4700:4700::1111]:443",
		"[2001:4860:4860::8888]:80", "[2001:4860:4860::8888]:443",
		"[2620:fe::fe]:80", "[2620:fe::fe]:443",
		"[2620:119:35::35]:80", "[2620:119:35::35]:443",
	}
	ipv4ProbeTarget = "1.1.1.1:80"
)

// ipv6Capability remembers whether the profile's server forwards IPv6. It never
// blocks: an unknown or stale answer starts a background probe, and IPv6 counts
// as available until a conclusive probe shows otherwise.
type ipv6Capability struct {
	mu          sync.Mutex
	unsupported bool
	checkedAt   time.Time
	retryAfter  time.Time
	probing     bool
	now         func() time.Time
}

func (c *ipv6Capability) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

// unavailable reports whether IPv6 is known not to work through the profile.
// probe returns whether IPv6 works and whether the answer is conclusive; it runs
// through submit so the device waits for it on close.
func (c *ipv6Capability) unavailable(submit func(func()) error, probe func() (supported, conclusive bool)) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	stale := c.checkedAt.IsZero() || (c.unsupported && c.clock().Sub(c.checkedAt) >= ipv6UnsupportedRetry)
	if stale && !c.probing && !c.clock().Before(c.retryAfter) {
		c.probing = true
		_ = submit(func() { c.run(probe) })
	}
	return c.unsupported
}

func (c *ipv6Capability) run(probe func() (supported, conclusive bool)) {
	supported, conclusive := probe()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.probing = false
	if conclusive {
		c.unsupported, c.checkedAt = !supported, c.clock()
	} else {
		c.retryAfter = c.clock().Add(ipv6InconclusiveRetry)
	}
}

// IPv6Unavailable tells the packet stack to refuse IPv6 connections at once,
// so applications fall back to IPv4 instead of waiting on a server that
// cannot reach IPv6 (issue #341).
func (d *OutlineDevice) IPv6Unavailable() bool {
	return d.ipv6.unavailable(d.Submit, d.probeIPv6)
}

func (d *OutlineDevice) probeIPv6() (supported, conclusive bool) {
	ctx, cancel := context.WithTimeout(d.ctx, ipv6ProbeTimeout)
	defer cancel()

	results := make(chan bool, len(ipv6ProbeTargets))
	for _, target := range ipv6ProbeTargets {
		_ = d.Submit(func() { results <- d.probeTarget(ctx, target) })
	}
	control := d.probeTarget(ctx, ipv4ProbeTarget)
	for range ipv6ProbeTargets {
		if <-results {
			log.Debugf(Category, "[SOCKS5 IPv6] profile forwards IPv6 destinations")
			return true, true
		}
	}
	if !control {
		log.Debugf(Category, "[SOCKS5 IPv6] probe inconclusive: IPv4 control target did not answer either")
		return false, false
	}
	log.Infof(Category, "[SOCKS5 IPv6] profile does not forward IPv6 destinations; refusing IPv6 connections so applications use IPv4")
	return false, true
}

func (d *OutlineDevice) probeTarget(ctx context.Context, target string) bool {
	conn, err := d.handleDial(ctx, networkTCP, target)
	if err != nil {
		return false
	}
	defer func() { _ = conn.Close() }()
	deadline, _ := ctx.Deadline()
	_ = conn.SetDeadline(deadline)
	if strings.HasSuffix(target, ":443") {
		// Only reachability matters, not the certificate: a finished handshake
		// means the endpoint answered through the profile.
		return tls.Client(conn, &tls.Config{InsecureSkipVerify: true}).HandshakeContext(ctx) == nil //nolint:gosec
	}
	if _, err := conn.Write([]byte(ipv6ProbeRequest)); err != nil {
		return false
	}
	buffer := make([]byte, 1)
	n, _ := conn.Read(buffer)
	return n > 0
}
