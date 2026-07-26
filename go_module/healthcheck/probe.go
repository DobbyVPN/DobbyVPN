package healthcheck

import (
	"context"
	"fmt"
	"go_module/dnscache"
	hcCommon "go_module/healthcheck/common"
	"go_module/log"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"
)

const (
	probeTimeout          = 2 * time.Second
	probeMinTimeout       = 100 * time.Millisecond
	probeMaxBodyBytes     = 4096
	httpProbeMinSuccesses = 2
	probeFailureResult    = int64(-1)
	probeDNSPreflightTTL  = 12 * time.Hour
)

var httpProbeURLs = []string{
	"https://www.google.com/generate_204",
	"https://www.cloudflare.com/cdn-cgi/trace",
	"https://about.google",
}

var probeDNSLookup = net.DefaultResolver.LookupIPAddr

type probeEndpointResult struct {
	url       string
	latencyMs int64
	status    int
	err       error
}

// PreflightTunnelProbeDNS resolves the fixed readiness hosts before a platform
// redirects DNS into the new tunnel. The cached IPv4 answers remove a circular
// dependency where proving the tunnel requires the tunnel's first DNS exchange
// to have already succeeded. Failures remain best-effort because a platform may
// still provide working DNS through the tunnel.
func PreflightTunnelProbeDNS(ctx context.Context) (resolved, total int) {
	hosts := tunnelProbeHosts(httpProbeURLs)
	for _, host := range hosts {
		if err := ctx.Err(); err != nil {
			break
		}
		lookupCtx, cancel := context.WithTimeout(ctx, probeTimeout)
		addresses, err := probeDNSLookup(lookupCtx, host)
		cancel()
		if err != nil {
			continue
		}
		for _, address := range addresses {
			if ipv4 := address.IP.To4(); ipv4 != nil {
				if dnscache.SetIPv4(host, ipv4.String(), "tunnel-probe-preflight", probeDNSPreflightTTL) {
					resolved++
				}
				break
			}
		}
	}
	log.Debugf(hcCommon.Category, "Tunnel probe DNS preflight resolved=%d total=%d", resolved, len(hosts))
	return resolved, len(hosts)
}

func tunnelProbeHosts(rawURLs []string) []string {
	seen := make(map[string]struct{}, len(rawURLs))
	hosts := make([]string, 0, len(rawURLs))
	for _, rawURL := range rawURLs {
		parsed, err := url.Parse(rawURL)
		if err != nil {
			continue
		}
		host := dnscache.NormalizeHost(parsed.Hostname())
		if host == "" || net.ParseIP(host) != nil {
			continue
		}
		if _, exists := seen[host]; exists {
			continue
		}
		seen[host] = struct{}{}
		hosts = append(hosts, host)
	}
	return hosts
}

// MeasureTunnelProbeAverageLatencyMillis runs protocol-selection probes through
// the currently installed system VPN route. Every request uses a fresh transport
// with keep-alives disabled so latency cannot be inherited from a previous
// protocol's pooled TCP/TLS connection.
func MeasureTunnelProbeAverageLatencyMillis() int64 {
	return MeasureTunnelProbeAverageLatencyMillisWithTimeout(int64(probeTimeout / time.Millisecond))
}

func MeasureTunnelProbeAverageLatencyMillisWithTimeout(timeoutMillis int64) int64 {
	return MeasureTunnelProbeAverageLatencyMillisWithContext(context.Background(), timeoutMillis)
}

// MeasureTunnelProbeAverageLatencyMillisWithContext runs the tunnel probe with
// the supplied cancellation context. The context is propagated to every
// endpoint request; timeoutMillis remains the per-endpoint upper bound for
// callers which do not have a tighter deadline.
func MeasureTunnelProbeAverageLatencyMillisWithContext(ctx context.Context, timeoutMillis int64) int64 {
	timeout := time.Duration(timeoutMillis) * time.Millisecond
	if timeout < probeMinTimeout {
		log.Warnf(hcCommon.Category, "Tunnel probe timeout is too small timeoutMs=%d using default=%s", timeoutMillis, probeTimeout)
		timeout = probeTimeout
	}
	log.Debugf(hcCommon.Category, "Tunnel probe begin endpoints=%d timeout=%s", len(httpProbeURLs), timeout)

	results := make([]probeEndpointResult, len(httpProbeURLs))
	var wg sync.WaitGroup
	for i, url := range httpProbeURLs {
		wg.Add(1)
		go func(i int, url string) {
			defer wg.Done()
			results[i] = probeEndpoint(ctx, url, timeout)
		}(i, url)
	}
	wg.Wait()

	var sum int64
	successes := 0
	for index, result := range results {
		if result.err != nil {
			log.Warnf(
				hcCommon.Category,
				"Tunnel probe endpoint failed endpoint=%d errorType=%T",
				index,
				result.err,
			)
			continue
		}
		successes++
		sum += result.latencyMs
		log.Debugf(
			hcCommon.Category,
			"Tunnel probe endpoint ok endpoint=%d latencyMs=%d status=%d",
			index,
			result.latencyMs,
			result.status,
		)
	}
	requiredSuccesses := httpProbeMinSuccesses
	if requiredSuccesses > len(httpProbeURLs) {
		requiredSuccesses = len(httpProbeURLs)
	}
	log.Debugf(hcCommon.Category, "Tunnel probe latency samples successful=%d/%d required=%d", successes, len(httpProbeURLs), requiredSuccesses)
	if successes < requiredSuccesses {
		log.Warnf(hcCommon.Category, "Tunnel probe failed: not enough latency endpoints succeeded passed=%d required=%d total=%d", successes, requiredSuccesses, len(httpProbeURLs))
		return probeFailureResult
	}
	if successes != len(httpProbeURLs) {
		log.Warnf(hcCommon.Category, "Tunnel probe continuing with partial latency quorum passed=%d total=%d", successes, len(httpProbeURLs))
	}

	avg := sum / int64(successes)
	log.Debugf(hcCommon.Category, "Tunnel probe finished averageLatencyMs=%d", avg)
	return avg
}

func probeEndpoint(parent context.Context, endpointURL string, timeout time.Duration) probeEndpointResult {
	startedAt := time.Now()
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	transport := &http.Transport{
		DialContext:         cachedDialContext(timeout, "healthcheck-probe"),
		DisableKeepAlives:   true,
		ForceAttemptHTTP2:   false,
		TLSHandshakeTimeout: timeout,
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   timeout,
	}
	defer client.CloseIdleConnections()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpointURL, http.NoBody)
	if err != nil {
		return probeEndpointResult{url: endpointURL, err: err}
	}
	req.Close = true
	req.Header.Set("Cache-Control", "no-store")
	req.Header.Set("Pragma", "no-cache")
	req.Header.Set("Connection", "close")

	resp, err := client.Do(req)
	if err != nil {
		return probeEndpointResult{url: endpointURL, err: err}
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			log.Warnf(hcCommon.Category, "Tunnel probe response body close failed errorType=%T", closeErr)
		}
	}()

	_, err = io.ReadAll(io.LimitReader(resp.Body, probeMaxBodyBytes))
	if err != nil {
		return probeEndpointResult{url: endpointURL, status: resp.StatusCode, err: err}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return probeEndpointResult{url: endpointURL, status: resp.StatusCode, err: fmt.Errorf("unexpected status %d", resp.StatusCode)}
	}

	result := probeEndpointResult{
		url:       endpointURL,
		latencyMs: maxInt64(1, time.Since(startedAt).Milliseconds()),
		status:    resp.StatusCode,
	}
	return result
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
