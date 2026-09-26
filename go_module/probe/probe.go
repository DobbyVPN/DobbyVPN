package probe

import (
	"context"
	"errors"
	"fmt"
	"go_module/log"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/proxy"
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

// MeasureTunnelProbeAverageLatencyMillisWithContext runs requests through the
// active protocol device's local SOCKS endpoint. It fails closed when that
// endpoint is missing or invalid. Every request uses a fresh transport with
// keep-alives disabled so latency cannot be inherited from another session.
func MeasureTunnelProbeAverageLatencyMillisWithContext(ctx context.Context, timeoutMillis int64, proxyAddr string) int64 {
	timeout := time.Duration(timeoutMillis) * time.Millisecond
	if timeout <= 0 {
		log.Warnf("PROBE", "Tunnel probe timeout is invalid timeoutMs=%d", timeoutMillis)
		return probeFailureResult
	}
	dialContext, err := socks5DialContext(proxyAddr, timeout)
	if err != nil {
		log.Warnf("PROBE", "Tunnel probe local SOCKS endpoint is unavailable errorClass=%s", probeErrorClass(err))
		return probeFailureResult
	}
	return measureTunnelProbe(ctx, timeout, dialContext)
}

func socks5DialContext(proxyAddr string, timeout time.Duration) (func(context.Context, string, string) (net.Conn, error), error) {
	if proxyAddr == "" {
		return nil, errors.New("empty SOCKS endpoint")
	}
	parsed, err := url.Parse("socks5://" + proxyAddr)
	if err != nil || parsed.Host == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("invalid SOCKS endpoint")
	}
	port := parsed.Port()
	if parsed.Hostname() == "" || port == "" {
		return nil, errors.New("SOCKS endpoint requires host and port")
	}
	var credentials *proxy.Auth
	if parsed.User != nil {
		credentials = &proxy.Auth{User: parsed.User.Username()}
		credentials.Password, _ = parsed.User.Password()
	}
	dialer, err := proxy.SOCKS5("tcp", net.JoinHostPort(parsed.Hostname(), port), credentials, &net.Dialer{Timeout: timeout, KeepAlive: -1})
	if err != nil {
		return nil, errors.New("create SOCKS dialer")
	}
	contextDialer, ok := dialer.(proxy.ContextDialer)
	if !ok {
		return nil, errors.New("SOCKS dialer does not support cancellation")
	}
	return contextDialer.DialContext, nil
}

func measureTunnelProbe(ctx context.Context, timeout time.Duration, dialContext func(context.Context, string, string) (net.Conn, error)) int64 {
	log.Debugf("PROBE", "Tunnel probe begin endpoints=%d timeout=%s", len(httpProbeURLs), timeout)

	results := make([]probeEndpointResult, len(httpProbeURLs))
	var wg sync.WaitGroup
	for i, url := range httpProbeURLs {
		wg.Add(1)
		go func(i int, url string) {
			defer wg.Done()
			results[i] = probeEndpoint(ctx, url, timeout, dialContext)
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

func probeEndpoint(parent context.Context, endpointURL string, timeout time.Duration, dialContext func(context.Context, string, string) (net.Conn, error)) probeEndpointResult {
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
		DialContext:         dialContext,
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
		return failedProbeEndpoint(0, probeStageRequest, err)
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
		return failedProbeEndpoint(0, probeFailureStage(stage.Load()), err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			log.Warnf("PROBE", "Tunnel probe response body close failed errorType=%T error=%v", closeErr, closeErr)
		}
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return failedProbeEndpoint(resp.StatusCode, probeStageStatus, fmt.Errorf("unexpected status %d", resp.StatusCode))
	}

	result := probeEndpointResult{
		latencyMs: maxInt64(1, time.Since(startedAt).Milliseconds()),
		status:    resp.StatusCode,
	}
	return result
}

func failedProbeEndpoint(status int, stage string, err error) probeEndpointResult {
	return probeEndpointResult{
		status: status, failureStage: stage, errorClass: probeErrorClass(err), err: err,
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
