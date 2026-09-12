package probe

import (
	"context"
	"errors"
	"fmt"
	"go_module/log"
	"net"
	"net/http"
	"net/http/httptrace"
	"sync"
	"sync/atomic"
	"time"
)

const (
	probeTimeout          = 2 * time.Second
	httpProbeMinSuccesses = 2
	probeFailureResult    = int64(-1)
)

var httpProbeURLs = []string{
	"https://www.google.com/generate_204",
	"https://www.cloudflare.com/cdn-cgi/trace",
	"https://about.google",
}

type probeEndpointResult struct {
	url          string
	latencyMs    int64
	status       int
	failureStage string
	errorClass   string
	err          error
}

const (
	probeStageRequest  = "request"
	probeStageConnect  = "connect"
	probeStageTLS      = "tls"
	probeStageResponse = "response"
	probeStageStatus   = "status"
	probeErrorTimeout  = "timeout"
	probeErrorCanceled = "canceled"
	probeErrorDNS      = "dns"
	probeErrorProtocol = "protocol"
)

// pingHostCheck performs one bounded HTTP readiness request through the
// generation-owned route. It is kept beside the latency probe so the package
// contains only pure probe operations; it does not own connection state or a
// background health-check lifecycle.
func pingHostCheck(host string) error {
	const timeout = 3 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, host, http.NoBody)
	if err != nil {
		return fmt.Errorf("probe request initialization failed: %w", err)
	}
	client := &http.Client{
		Transport: &http.Transport{
			DialContext:         cachedDialContext(timeout, "session-probe-http"),
			DisableKeepAlives:   true,
			ForceAttemptHTTP2:   false,
			TLSHandshakeTimeout: timeout,
		},
		Timeout: timeout,
	}
	defer client.CloseIdleConnections()
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("probe request failed: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			log.Warnf("PROBE", "HTTP probe response body close failed errorType=%T error=%v", closeErr, closeErr)
		}
	}()
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return fmt.Errorf("probe returned status %d", resp.StatusCode)
	}
	return nil
}

func quorumHTTPPingCheck(hosts []string) error {
	if len(hosts) == 0 {
		return errors.New("probe has no HTTP candidates")
	}
	successes := 0
	var failures []error
	for _, host := range hosts {
		if err := pingHostCheck(host); err != nil {
			failures = append(failures, err)
			continue
		}
		successes++
	}
	required := httpProbeMinSuccesses
	if required > len(hosts) {
		required = len(hosts)
	}
	if successes < required {
		return fmt.Errorf("probe HTTP quorum failed passed=%d required=%d total=%d: %w", successes, required, len(hosts), errors.Join(failures...))
	}
	if successes != len(hosts) {
		log.Warnf("PROBE", "probe HTTP quorum partial passed=%d total=%d", successes, len(hosts))
	}
	return nil
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
	if timeout <= 0 {
		log.Warnf("PROBE", "Tunnel probe timeout is invalid timeoutMs=%d", timeoutMillis)
		return probeFailureResult
	}
	log.Debugf("PROBE", "Tunnel probe begin endpoints=%d timeout=%s", len(httpProbeURLs), timeout)

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
				"PROBE",
				"Tunnel probe target failed targetOrdinal=%d stage=%s errorClass=%s",
				index,
				result.failureStage,
				result.errorClass,
			)
			continue
		}
		successes++
		sum += result.latencyMs
		log.Debugf(
			"PROBE",
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
	log.Debugf("PROBE", "Tunnel probe latency samples successful=%d/%d required=%d", successes, len(httpProbeURLs), requiredSuccesses)
	if successes < requiredSuccesses {
		log.Warnf("PROBE", "Tunnel probe failed: not enough latency endpoints succeeded passed=%d required=%d total=%d", successes, requiredSuccesses, len(httpProbeURLs))
		return probeFailureResult
	}
	if successes != len(httpProbeURLs) {
		log.Warnf("PROBE", "Tunnel probe continuing with partial latency quorum passed=%d total=%d", successes, len(httpProbeURLs))
	}

	avg := sum / int64(successes)
	log.Debugf("PROBE", "Tunnel probe finished averageLatencyMs=%d", avg)
	return avg
}

func probeEndpoint(parent context.Context, endpointURL string, timeout time.Duration) probeEndpointResult {
	startedAt := time.Now()
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	var stage atomic.Int32
	setStage := func(next int32) {
		for {
			current := stage.Load()
			if next <= current || stage.CompareAndSwap(current, next) {
				return
			}
		}
	}

	transport := &http.Transport{
		DialContext:         cachedDialContext(timeout, "session-probe"),
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
		return failedProbeEndpoint(endpointURL, 0, probeStageRequest, err)
	}
	trace := &httptrace.ClientTrace{
		ConnectStart:         func(_, _ string) { setStage(1) },
		TLSHandshakeStart:    func() { setStage(2) },
		WroteRequest:         func(httptrace.WroteRequestInfo) { setStage(3) },
		GotFirstResponseByte: func() { setStage(4) },
	}
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))
	req.Close = true
	req.Header.Set("Cache-Control", "no-store")
	req.Header.Set("Pragma", "no-cache")
	req.Header.Set("Connection", "close")

	resp, err := client.Do(req)
	if err != nil {
		return failedProbeEndpoint(endpointURL, 0, probeFailureStage(stage.Load()), err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			log.Warnf("PROBE", "Tunnel probe response body close failed errorType=%T error=%v", closeErr, closeErr)
		}
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return failedProbeEndpoint(endpointURL, resp.StatusCode, probeStageStatus, fmt.Errorf("unexpected status %d", resp.StatusCode))
	}

	result := probeEndpointResult{
		url:       endpointURL,
		latencyMs: maxInt64(1, time.Since(startedAt).Milliseconds()),
		status:    resp.StatusCode,
	}
	return result
}

func failedProbeEndpoint(endpointURL string, status int, stage string, err error) probeEndpointResult {
	return probeEndpointResult{
		url: endpointURL, status: status, failureStage: stage,
		errorClass: probeErrorClass(err), err: err,
	}
}

func probeFailureStage(stage int32) string {
	switch stage {
	case 1:
		return probeStageConnect
	case 2:
		return probeStageTLS
	case 3:
		return probeStageResponse
	case 4:
		return probeStageResponse
	default:
		return probeStageRequest
	}
}

func probeErrorClass(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return probeErrorTimeout
	case errors.Is(err, context.Canceled):
		return probeErrorCanceled
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return probeErrorDNS
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return probeErrorTimeout
		}
		return "network"
	}
	return probeErrorProtocol
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
