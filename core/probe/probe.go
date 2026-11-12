package probe

import (
	"context"
	"core/log"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"sync/atomic"
	"time"

	"golang.org/x/net/proxy"
)

const (
	probeTimeout          = 2 * time.Second
	httpProbeMinSuccesses = 2
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
	bytesUp      int64
	bytesDown    int64
	err          error
}

const (
	probeStageRequest  = "request"
	probeStageConnect  = "connect"
	probeStageTLS      = "tls"
	probeStageResponse = "response"
	probeStageStatus   = "status"

	probeErrorTimeout         = "timeout"
	probeErrorCanceled        = "canceled"
	probeErrorCanceledSurplus = "canceled_surplus"
	probeErrorDNS             = "dns"
	probeErrorProtocol        = "protocol"
)

// errProbeDecided is the cancel cause used when the quorum decision is made.
// Endpoints still pending are surplus work: their cancellation is deliberate
// and is reported as canceled_surplus, distinct from a real endpoint failure.
var errProbeDecided = errors.New("readiness probe decision reached")

// probeStreamCountsKey carries the per-request byte counters into the dial
// used by that request, so an outcome can report how far its stream advanced.
type probeStreamCountsKey struct{}

type streamByteCounts struct {
	up   atomic.Int64
	down atomic.Int64
}

// countingProbeConn counts stream bytes so a failed readiness outcome can say
// whether the endpoint carried any data before it failed.
type countingProbeConn struct {
	net.Conn
	counts *streamByteCounts
}

func (c countingProbeConn) Read(p []byte) (int, error) {
	read, err := c.Conn.Read(p)
	c.counts.down.Add(int64(read))
	return read, err
}

func (c countingProbeConn) Write(p []byte) (int, error) {
	written, err := c.Conn.Write(p)
	c.counts.up.Add(int64(written))
	return written, err
}

func (c countingProbeConn) CloseWrite() error {
	if writer, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return writer.CloseWrite()
	}
	return nil
}

// instrumentProbeDial wraps every connection whose request carries byte
// counters; other dials pass through unchanged.
func instrumentProbeDial(dial func(context.Context, string, string) (net.Conn, error)) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, err := dial(ctx, network, address)
		if err != nil {
			return nil, err
		}
		counts, _ := ctx.Value(probeStreamCountsKey{}).(*streamByteCounts)
		if counts == nil {
			return conn, nil
		}
		return countingProbeConn{Conn: conn, counts: counts}, nil
	}
}

// surplusCanceled reports whether an endpoint was canceled by the probe's own
// quorum decision rather than by its caller or its own timeout. A causal
// cancellation reaches the transport as the cancel cause, so both forms count.
func surplusCanceled(ctx context.Context, err error) bool {
	if err == nil || !errors.Is(context.Cause(ctx), errProbeDecided) {
		return false
	}
	return errors.Is(err, context.Canceled) || errors.Is(err, errProbeDecided)
}

// logProbeEndpointOutcome writes one endpoint's outcome where that endpoint
// completes, so results the quorum decision never consumes stay in the log.
func logProbeEndpointOutcome(index int, result probeEndpointResult) {
	if result.err == nil {
		log.Debugf("PROBE", "Tunnel probe endpoint ok endpoint=%d latencyMs=%d status=%d bytesUp=%d bytesDown=%d",
			index, result.latencyMs, result.status, result.bytesUp, result.bytesDown)
		return
	}
	writeLog := log.Warnf
	if result.errorClass == probeErrorCanceled || result.errorClass == probeErrorCanceledSurplus || errors.Is(result.err, context.Canceled) {
		writeLog = log.Debugf
	}
	writeLog("PROBE", "Tunnel probe target failed targetOrdinal=%d stage=%s status=%d errorClass=%s error=%v bytesUp=%d bytesDown=%d",
		index, result.failureStage, result.status, result.errorClass, result.err, result.bytesUp, result.bytesDown)
}

// MeasureTunnelProbeAverageLatencyMillisWithContext runs requests through the
// active protocol device's local SOCKS endpoint. It fails closed when that
// endpoint is missing or invalid. Every request uses a fresh transport with
// keep-alives disabled so latency cannot be inherited from another session.
func MeasureTunnelProbeAverageLatencyMillisWithContext(ctx context.Context, timeoutMillis int64, proxyAddr string) (int64, error) {
	timeout := time.Duration(timeoutMillis) * time.Millisecond
	if timeout <= 0 {
		return 0, fmt.Errorf("tunnel probe timeout must be positive: %dms", timeoutMillis)
	}
	dialContext, err := socks5DialContext(proxyAddr, timeout)
	if err != nil {
		return 0, fmt.Errorf("tunnel probe local SOCKS endpoint is unavailable: %w", err)
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
		return nil, fmt.Errorf("create SOCKS dialer: %w", err)
	}
	contextDialer, ok := dialer.(proxy.ContextDialer)
	if !ok {
		return nil, errors.New("SOCKS dialer does not support cancellation")
	}
	return contextDialer.DialContext, nil
}

func measureTunnelProbe(ctx context.Context, timeout time.Duration, dialContext func(context.Context, string, string) (net.Conn, error)) (int64, error) {
	if len(httpProbeURLs) == 0 {
		return 0, errors.New("tunnel probe has no endpoints")
	}
	log.Debugf("PROBE", "Tunnel probe begin endpoints=%d timeout=%s", len(httpProbeURLs), timeout)

	// The readiness rule is a quorum, not a full sweep. Cancelling the derived
	// context aborts any endpoint still pending once the decision is made, so a
	// straggling third endpoint cannot hold the connect open.
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(context.Canceled)

	type completedProbe struct {
		index  int
		result probeEndpointResult
	}
	probeDial := instrumentProbeDial(dialContext)
	completed := make(chan completedProbe, len(httpProbeURLs))
	for i, url := range httpProbeURLs {
		go func(i int, url string) {
			result := probeEndpoint(ctx, url, timeout, probeDial)
			// Log where the endpoint completes: once the quorum is decided the
			// consumer stops reading, and these outcomes must survive that.
			logProbeEndpointOutcome(i, result)
			completed <- completedProbe{index: i, result: result}
		}(i, url)
	}

	requiredSuccesses := httpProbeMinSuccesses
	if requiredSuccesses > len(httpProbeURLs) {
		requiredSuccesses = len(httpProbeURLs)
	}

	var sum int64
	successes := 0
	var endpointErrors []error
	for finished := range len(httpProbeURLs) {
		received := <-completed
		result := received.result
		if result.err != nil {
			endpointErr := fmt.Errorf(
				"endpoint %d failed at %s (status=%d, error_class=%s): %w",
				received.index,
				result.failureStage,
				result.status,
				result.errorClass,
				result.err,
			)
			endpointErrors = append(endpointErrors, endpointErr)
		} else {
			successes++
			sum += result.latencyMs
		}
		if successes >= requiredSuccesses {
			avg := sum / int64(successes)
			log.Debugf("PROBE", "Tunnel probe latency samples successful=%d/%d required=%d", successes, len(httpProbeURLs), requiredSuccesses)
			if finished+1 == len(httpProbeURLs) {
				if successes != len(httpProbeURLs) {
					log.Warnf("PROBE", "Tunnel probe continuing with partial latency quorum passed=%d total=%d", successes, len(httpProbeURLs))
				}
			} else {
				log.Debugf("PROBE", "Tunnel probe quorum reached before all endpoints completed successful=%d measured=%d total=%d", successes, finished+1, len(httpProbeURLs))
			}
			log.Debugf("PROBE", "Tunnel probe finished averageLatencyMs=%d", avg)
			cancel(errProbeDecided)
			return avg, nil
		}
		if successes+len(httpProbeURLs)-(finished+1) < requiredSuccesses {
			break
		}
	}

	log.Debugf("PROBE", "Tunnel probe latency samples successful=%d/%d required=%d", successes, len(httpProbeURLs), requiredSuccesses)
	writeLog := log.Warnf
	if errors.Is(ctx.Err(), context.Canceled) {
		writeLog = log.Debugf
	}
	writeLog("PROBE", "Tunnel probe failed: not enough latency endpoints succeeded passed=%d required=%d total=%d", successes, requiredSuccesses, len(httpProbeURLs))
	cancel(errProbeDecided)
	return 0, fmt.Errorf(
		"tunnel probe reached %d/%d successful endpoints; %d required: %w",
		successes,
		len(httpProbeURLs),
		requiredSuccesses,
		errors.Join(endpointErrors...),
	)
}

func probeEndpoint(parent context.Context, endpointURL string, timeout time.Duration, dialContext func(context.Context, string, string) (net.Conn, error)) probeEndpointResult {
	startedAt := time.Now()
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	counts := &streamByteCounts{}
	ctx = context.WithValue(ctx, probeStreamCountsKey{}, counts)
	// Every return reports the stream progress this endpoint reached and marks
	// a cancellation caused by the probe's own quorum decision as surplus work.
	finish := func(result probeEndpointResult) probeEndpointResult {
		result.bytesUp, result.bytesDown = counts.up.Load(), counts.down.Load()
		if surplusCanceled(ctx, result.err) {
			result.errorClass = probeErrorCanceledSurplus
		}
		return result
	}
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
		return finish(failedProbeEndpoint(0, probeStageRequest, err))
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
		return finish(failedProbeEndpoint(0, probeFailureStage(stage.Load()), err))
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			log.Warnf("PROBE", "Tunnel probe response body close failed errorType=%T error=%v", closeErr, closeErr)
		}
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return finish(failedProbeEndpoint(resp.StatusCode, probeStageStatus, fmt.Errorf("unexpected status %d", resp.StatusCode)))
	}

	result := probeEndpointResult{
		latencyMs: maxInt64(1, time.Since(startedAt).Milliseconds()),
		status:    resp.StatusCode,
	}
	return finish(result)
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
