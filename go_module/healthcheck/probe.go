package healthcheck

import (
	"context"
	"fmt"
	hcCommon "go_module/healthcheck/common"
	"go_module/log"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	probeTimeout          = 2 * time.Second
	probeMinTimeout       = 100 * time.Millisecond
	probeIPifyURL         = "https://api.ipify.org/"
	probeMaxBodyBytes     = 4096
	httpProbeMinSuccesses = 2
	probeFailureResult    = int64(-1)
)

var httpProbeURLs = []string{
	"https://www.google.com/generate_204",
	"https://www.cloudflare.com/cdn-cgi/trace",
	"https://about.google",
}

type probeEndpointResult struct {
	url       string
	body      string
	latencyMs int64
	status    int
	err       error
}

// MeasureTunnelProbeAverageLatencyMillis runs protocol-selection probes through
// the currently installed system VPN route. Every request uses a fresh transport
// with keep-alives disabled so latency cannot be inherited from a previous
// protocol's pooled TCP/TLS connection.
func MeasureTunnelProbeAverageLatencyMillis() int64 {
	return MeasureTunnelProbeAverageLatencyMillisWithTimeout(int64(probeTimeout / time.Millisecond))
}

func MeasureTunnelProbeAverageLatencyMillisWithTimeout(timeoutMillis int64) int64 {
	timeout := time.Duration(timeoutMillis) * time.Millisecond
	if timeout < probeMinTimeout {
		log.Warnf(hcCommon.Category, "Tunnel probe timeout is too small timeoutMs=%d using default=%s", timeoutMillis, probeTimeout)
		timeout = probeTimeout
	}
	log.Debugf(hcCommon.Category, "Tunnel probe begin endpoints=%d ipify=%s timeout=%s", len(httpProbeURLs), probeIPifyURL, timeout)

	results := make([]probeEndpointResult, len(httpProbeURLs))
	var wg sync.WaitGroup
	for i, url := range httpProbeURLs {
		wg.Add(1)
		go func(i int, url string) {
			defer wg.Done()
			results[i] = probeEndpoint(url, false, timeout)
		}(i, url)
	}
	wg.Wait()

	var sum int64
	successes := 0
	for _, result := range results {
		if result.err != nil {
			log.Warnf(hcCommon.Category, "Tunnel probe endpoint failed url=%s error=%v", result.url, result.err)
			continue
		}
		successes++
		sum += result.latencyMs
		log.Debugf(hcCommon.Category, "Tunnel probe endpoint ok url=%s latencyMs=%d status=%d", result.url, result.latencyMs, result.status)
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

	ipify := probeEndpoint(probeIPifyURL, true, timeout)
	switch {
	case ipify.err != nil:
		log.Warnf(hcCommon.Category, "Tunnel probe ipify failed url=%s error=%v", ipify.url, ipify.err)
	case net.ParseIP(ipify.body) == nil:
		log.Warnf(hcCommon.Category, "Tunnel probe ipify failed url=%s invalid_public_ip=%q", ipify.url, ipify.body)
	default:
		log.Infof(hcCommon.Category, "Tunnel probe ipify ok url=%s publicIP=%s latencyMs=%d status=%d", ipify.url, ipify.body, ipify.latencyMs, ipify.status)
	}

	avg := sum / int64(successes)
	log.Debugf(hcCommon.Category, "Tunnel probe finished averageLatencyMs=%d", avg)
	return avg
}

func probeEndpoint(url string, keepBody bool, timeout time.Duration) probeEndpointResult {
	startedAt := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
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

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return probeEndpointResult{url: url, err: err}
	}
	req.Close = true
	req.Header.Set("Cache-Control", "no-store")
	req.Header.Set("Pragma", "no-cache")
	req.Header.Set("Connection", "close")

	resp, err := client.Do(req)
	if err != nil {
		return probeEndpointResult{url: url, err: err}
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			log.Warnf(hcCommon.Category, "Tunnel probe response body close failed url=%s error=%v", url, closeErr)
		}
	}()

	body, err := io.ReadAll(io.LimitReader(resp.Body, probeMaxBodyBytes))
	if err != nil {
		return probeEndpointResult{url: url, status: resp.StatusCode, err: err}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return probeEndpointResult{url: url, status: resp.StatusCode, err: fmt.Errorf("unexpected status %d", resp.StatusCode)}
	}

	result := probeEndpointResult{
		url:       url,
		latencyMs: maxInt64(1, time.Since(startedAt).Milliseconds()),
		status:    resp.StatusCode,
	}
	if keepBody {
		result.body = strings.TrimSpace(string(body))
	}
	return result
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
