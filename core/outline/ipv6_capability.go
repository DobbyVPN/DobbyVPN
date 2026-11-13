package outline

import (
	"context"
	"sync"
	"time"

	"core/log"
)

const (
	// ipv6ProbeTimeout bounds one end-to-end probe through the profile.
	ipv6ProbeTimeout = 4 * time.Second
	// ipv6UnsupportedRetry is how long a failed probe is trusted. The server's
	// IPv6 egress rarely changes inside a session, but a probe can fail for a
	// transient reason, so IPv6 is tried again after this interval.
	ipv6UnsupportedRetry = time.Minute
	ipv6ProbeRequest     = "HEAD / HTTP/1.0\r\n\r\n"
)

// ipv6ProbeTargets are plain HTTP endpoints that answer any request over IPv6.
// A reply proves that the server forwards IPv6 destinations; a successful
// stream dial alone does not, because Shadowsocks only connects to the server.
var ipv6ProbeTargets = []string{
	"[2606:4700:4700::1111]:80",
	"[2606:4700:4700::1001]:80",
}

type ipv6ProbeKey struct{}

// ipv6Capability remembers whether the profile's server can reach IPv6
// destinations. The zero value is ready to use and starts as unknown.
type ipv6Capability struct {
	mu        sync.Mutex
	known     bool
	supported bool
	checkedAt time.Time
	inflight  chan struct{}
	now       func() time.Time
}

func (c *ipv6Capability) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

// usable reports whether IPv6 destinations should be dialed through the
// profile. The first caller runs probe; concurrent callers wait for its result.
// A caller whose context ends while waiting gets false without recording
// anything.
func (c *ipv6Capability) usable(ctx context.Context, probe func() bool) bool {
	for {
		c.mu.Lock()
		if c.known && (c.supported || c.clock().Sub(c.checkedAt) < ipv6UnsupportedRetry) {
			supported := c.supported
			c.mu.Unlock()
			return supported
		}
		if wait := c.inflight; wait != nil {
			c.mu.Unlock()
			select {
			case <-wait:
				continue
			case <-ctx.Done():
				return false
			}
		}
		done := make(chan struct{})
		c.inflight = done
		c.mu.Unlock()

		supported := probe()

		c.mu.Lock()
		c.inflight = nil
		// A probe interrupted by shutdown says nothing about the server.
		if ctx.Err() == nil {
			c.known = true
			c.supported = supported
			c.checkedAt = c.clock()
		}
		c.mu.Unlock()
		close(done)
		return supported
	}
}

// probeIPv6 sends a request to each target through the profile and reports
// whether any of them answered.
func (d *OutlineDevice) probeIPv6() bool {
	ctx, cancel := context.WithTimeout(context.WithValue(d.ctx, ipv6ProbeKey{}, true), ipv6ProbeTimeout)
	defer cancel()

	results := make(chan bool, len(ipv6ProbeTargets))
	for _, target := range ipv6ProbeTargets {
		go func(target string) { results <- d.probeIPv6Target(ctx, target) }(target)
	}
	for range ipv6ProbeTargets {
		if <-results {
			log.Debugf(Category, "[SOCKS5 IPv6] profile forwards IPv6 destinations")
			return true
		}
	}
	log.Infof(Category, "[SOCKS5 IPv6] profile does not forward IPv6 destinations; answering IPv6 connects immediately so applications use IPv4")
	return false
}

func (d *OutlineDevice) probeIPv6Target(ctx context.Context, target string) bool {
	conn, err := d.handleDial(ctx, networkTCP, target)
	if err != nil {
		return false
	}
	defer conn.Close()
	deadline, _ := ctx.Deadline()
	_ = conn.SetDeadline(deadline)
	if _, err := conn.Write([]byte(ipv6ProbeRequest)); err != nil {
		return false
	}
	buffer := make([]byte, 1)
	n, _ := conn.Read(buffer)
	return n > 0
}
