package runtime

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go_module/protocol"
	v1 "go_module/sessionapi/v2"
)

func profile() v1.RuntimeProfile {
	return v1.RuntimeProfile{
		Summary:          v1.ProfileSummary{Protocol: v1.ProtocolOutline},
		NormalizedFormat: v1.ConfigTransportURL,
		NormalizedConfig: []byte("normalized-only"),
		ExcludeCIDRs:     []string{"203.0.113.0/24"},
		PreflightHosts:   []string{"example.invalid"},
	}
}

type recorded struct {
	mu    sync.Mutex
	items []string
}

func (r *recorded) add(item string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items = append(r.items, item)
}
func (r *recorded) got() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.items...)
}

type fakeInputs struct {
	record *recorded
	err    error
}

func (f fakeInputs) Apply(_ context.Context, _ v1.SessionRef, cidrs, hosts []string) (InputLease, error) {
	if f.err != nil {
		return nil, f.err
	}
	if len(cidrs) != 1 || len(hosts) != 1 {
		return nil, errors.New("runtime did not supply Go-only inputs")
	}
	f.record.add("inputs")
	return inputRelease{f.record}, nil
}

type inputRelease struct{ record *recorded }

func (l inputRelease) Release(context.Context) error { l.record.add("inputs-stop"); return nil }

type fakeCore struct {
	record        *recorded
	connectErr    error
	disconnectErr error
	block         <-chan struct{}
	stopBlock     <-chan struct{}
	stopEntered   chan<- struct{}
}

func (c fakeCore) Connect() error {
	c.record.add("connect")
	if c.block != nil {
		<-c.block
	}
	return c.connectErr
}
func (c fakeCore) Disconnect() error {
	c.record.add("core-stop")
	if c.stopEntered != nil {
		c.stopEntered <- struct{}{}
	}
	if c.stopBlock != nil {
		<-c.stopBlock
	}
	return c.disconnectErr
}

type fakeDevice struct{}

func (fakeDevice) Open(int, string) error { return nil }
func (fakeDevice) GetProxyAddr() string   { return "127.0.0.1:1" }
func (fakeDevice) GetServerIP() net.IP    { return nil }
func (fakeDevice) Close() error           { return nil }

func options(record *recorded) Options {
	return Options{
		Inputs: fakeInputs{record: record},
		NewDevice: func(_ context.Context, _ v1.SessionRef, got v1.RuntimeProfile, _ SocketProtector) (protocol.ProtocolDevice, error) {
			if string(got.NormalizedConfig) != "normalized-only" {
				return nil, errors.New("device did not receive normalized config")
			}
			record.add("device")
			return fakeDevice{}, nil
		},
		NewCore: func(protocol.ProtocolDevice, io.ReadWriteCloser) sessionCore {
			return fakeCore{record: record}
		},
		Probe: func(context.Context) (int64, error) { record.add("probe"); return 7, nil },
		InitialReadiness: func(context.Context, v1.SessionRef) error {
			return nil
		},
		ConnectedHealth: func(ctx context.Context, _ v1.SessionRef) error {
			<-ctx.Done()
			return ctx.Err()
		},
	}
}

func TestStartWaitsForInitialReadinessAndRetries(t *testing.T) {
	record := &recorded{}
	o := options(record)
	o.ReadinessAttempts = 3
	o.ReadinessRetryInterval = time.Nanosecond
	attempts := 0
	o.InitialReadiness = func(context.Context, v1.SessionRef) error {
		attempts++
		record.add("ready")
		if attempts < 3 {
			return errors.New("not ready")
		}
		return nil
	}

	lease, err := New(o).Start(context.Background(), v1.SessionRef{Generation: 1}, profile())
	if err != nil {
		t.Fatal(err)
	}
	if attempts != 3 {
		t.Fatalf("readiness attempts=%d, want 3", attempts)
	}
	if err := lease.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{"inputs", "device", "connect", "ready", "ready", "ready", "core-stop", "inputs-stop"}
	if got := record.got(); !same(got, want) {
		t.Fatalf("order=%v, want=%v", got, want)
	}
}

func TestInitialReadinessFailureRollsBackLIFO(t *testing.T) {
	record := &recorded{}
	o := options(record)
	o.ReadinessAttempts = 2
	o.ReadinessRetryInterval = time.Nanosecond
	o.InitialReadiness = func(context.Context, v1.SessionRef) error {
		record.add("ready")
		return errors.New("not ready")
	}

	if _, err := New(o).Start(context.Background(), v1.SessionRef{Generation: 2}, profile()); err == nil {
		t.Fatal("Start succeeded without tunnel readiness")
	}
	want := []string{"inputs", "device", "connect", "ready", "ready", "core-stop", "inputs-stop"}
	if got := record.got(); !same(got, want) {
		t.Fatalf("order=%v, want=%v", got, want)
	}
}

func TestInitialReadinessCancellationRollsBackLIFO(t *testing.T) {
	record := &recorded{}
	o := options(record)
	entered := make(chan struct{})
	o.InitialReadiness = func(ctx context.Context, _ v1.SessionRef) error {
		close(entered)
		<-ctx.Done()
		return ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := New(o).Start(ctx, v1.SessionRef{Generation: 3}, profile())
		result <- err
	}()
	<-entered
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v, want cancellation", err)
	}
	want := []string{"inputs", "device", "connect", "core-stop", "inputs-stop"}
	if got := record.got(); !same(got, want) {
		t.Fatalf("order=%v, want=%v", got, want)
	}
}

func TestInitialReadinessAttemptTimeoutIsBounded(t *testing.T) {
	record := &recorded{}
	o := options(record)
	o.ReadinessAttempts = 1
	o.ReadinessAttemptTimeout = time.Millisecond
	o.InitialReadiness = func(ctx context.Context, _ v1.SessionRef) error {
		<-ctx.Done()
		return ctx.Err()
	}
	started := time.Now()
	_, err := New(o).Start(context.Background(), v1.SessionRef{Generation: 4}, profile())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v, want readiness deadline", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("readiness timeout took %s", elapsed)
	}
	want := []string{"inputs", "device", "connect", "core-stop", "inputs-stop"}
	if got := record.got(); !same(got, want) {
		t.Fatalf("order=%v, want=%v", got, want)
	}
}

func TestSecondStartWaitsUntilReadinessRollbackCleanupCompletes(t *testing.T) {
	record := &recorded{}
	cleanupRelease := make(chan struct{})
	cleanupEntered := make(chan struct{}, 1)
	o := options(record)
	o.ReadinessAttempts = 1
	o.InitialReadiness = func(_ context.Context, ref v1.SessionRef) error {
		if ref.Generation == 1 {
			return errors.New("not ready")
		}
		return nil
	}
	o.NewCore = func(protocol.ProtocolDevice, io.ReadWriteCloser) sessionCore {
		return fakeCore{
			record:      record,
			stopBlock:   cleanupRelease,
			stopEntered: cleanupEntered,
		}
	}
	r := New(o)

	first := make(chan error, 1)
	go func() {
		_, err := r.Start(context.Background(), v1.SessionRef{Generation: 1}, profile())
		first <- err
	}()
	<-cleanupEntered

	second := make(chan error, 1)
	var secondLease v1.RuntimeLease
	go func() {
		lease, err := r.Start(context.Background(), v1.SessionRef{Generation: 2}, profile())
		secondLease = lease
		second <- err
	}()
	select {
	case err := <-second:
		t.Fatalf("second Start completed before cleanup release: %v", err)
	case <-time.After(25 * time.Millisecond):
	}

	close(cleanupRelease)
	if err := <-first; err == nil {
		t.Fatal("first Start unexpectedly succeeded")
	}
	if err := <-second; err != nil {
		t.Fatalf("second Start after cleanup failed: %v", err)
	}
	if secondLease == nil {
		t.Fatal("second Start returned a nil lease")
	}
	if err := secondLease.Stop(context.Background()); err != nil {
		t.Fatalf("stop second lease: %v", err)
	}
}

func TestConnectedHealthMonitorAppliesThresholdWithoutSleeping(t *testing.T) {
	record := &recorded{}
	checks := make(chan error)
	entered := make(chan struct{}, 3)
	o := options(record)
	o.HealthInterval = time.Nanosecond
	o.HealthFailureThreshold = 2
	o.ConnectedHealth = func(ctx context.Context, _ v1.SessionRef) error {
		select {
		case entered <- struct{}{}:
		case <-ctx.Done():
			return ctx.Err()
		}
		select {
		case err := <-checks:
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	lease, err := New(o).Start(context.Background(), v1.SessionRef{Generation: 1}, profile())
	if err != nil {
		t.Fatal(err)
	}
	monitored, ok := lease.(v1.HealthMonitoringLease)
	if !ok {
		t.Fatal("runtime lease does not expose connected health monitoring")
	}
	<-entered
	checks <- errors.New("first failed check")
	<-entered
	checks <- errors.New("second failed check")
	select {
	case <-monitored.HealthFailures():
	case <-time.After(time.Second):
		t.Fatal("health monitor did not reach its failure threshold")
	}
	if err := lease.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestConnectedHealthMonitorSupportsThreeFailureThreshold(t *testing.T) {
	record := &recorded{}
	checks := make(chan error)
	entered := make(chan struct{}, 4)
	o := options(record)
	o.HealthInterval = time.Nanosecond
	o.HealthFailureThreshold = 3
	o.ConnectedHealth = func(ctx context.Context, _ v1.SessionRef) error {
		select {
		case entered <- struct{}{}:
		case <-ctx.Done():
			return ctx.Err()
		}
		select {
		case err := <-checks:
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	lease, err := New(o).Start(context.Background(), v1.SessionRef{Generation: 1}, profile())
	if err != nil {
		t.Fatal(err)
	}
	monitored := lease.(v1.HealthMonitoringLease)
	for attempt := 1; attempt <= 2; attempt++ {
		<-entered
		checks <- errors.New("transient failed check")
		select {
		case <-monitored.HealthFailures():
			t.Fatalf("health monitor failed after only %d checks", attempt)
		default:
		}
	}
	<-entered
	checks <- errors.New("third failed check")
	select {
	case <-monitored.HealthFailures():
	case <-time.After(time.Second):
		t.Fatal("health monitor did not reach its configured failure threshold")
	}
	if err := lease.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultHealthFailureThresholdRequiresThreeConsecutiveFailures(t *testing.T) {
	if got := defaultHealthFailureThreshold(); got != 3 {
		t.Fatalf("default health failure threshold = %d, want 3", got)
	}
}

func TestProductRuntimeHasNoHarnessHealthFaultCoupling(t *testing.T) {
	legacyName := strings.Join([]string{
		"DOBBYVPN", "HARDENING", "TEST", "FAIL", "HEALTH", "AFTER", "SUCCESSFUL", "CHECKS",
	}, "_")
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		content, err := os.ReadFile(filepath.Join(".", path))
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		text := string(content)
		if strings.Contains(text, legacyName) {
			t.Fatalf("product runtime file %s retains legacy environment name", path)
		}
		if strings.Contains(text, "Harness") || strings.Contains(text, "Torturer") {
			t.Fatalf("product runtime file %s contains private test-harness coupling", path)
		}
	}
}

func TestLegacyHarnessHealthFaultVariableIsIgnored(t *testing.T) {
	legacyName := strings.Join([]string{
		"DOBBYVPN", "HARDENING", "TEST", "FAIL", "HEALTH", "AFTER", "SUCCESSFUL", "CHECKS",
	}, "_")
	t.Setenv(legacyName, "1")
	checks := 0
	o := options(&recorded{})
	o.ConnectedHealth = func(context.Context, v1.SessionRef) error {
		checks++
		return nil
	}
	r := New(o).(*runtime)
	if r.options.HealthInterval != 10*time.Second || r.options.HealthFailureThreshold != 3 {
		t.Fatalf("legacy environment changed product defaults: interval=%s threshold=%d", r.options.HealthInterval, r.options.HealthFailureThreshold)
	}
	if err := r.options.ConnectedHealth(context.Background(), v1.SessionRef{Generation: 1}); err != nil {
		t.Fatal(err)
	}
	if checks != 1 {
		t.Fatalf("custom health seam calls=%d, want 1", checks)
	}
}

func TestExplicitHealthFaultSeamLeavesInitialReadinessUntouched(t *testing.T) {
	o := options(&recorded{})
	initialCalls := 0
	o.InitialReadiness = func(context.Context, v1.SessionRef) error {
		initialCalls++
		return nil
	}
	healthCalls := 0
	o.ConnectedHealth = func(context.Context, v1.SessionRef) error {
		healthCalls++
		if healthCalls > 1 {
			return errors.New("test health fault after 1 successful check")
		}
		return nil
	}
	o.HealthInterval = time.Second
	o.HealthFailureThreshold = 1
	r := New(o).(*runtime)

	if err := r.options.InitialReadiness(context.Background(), v1.SessionRef{Generation: 1}); err != nil {
		t.Fatalf("initial readiness was faulted: %v", err)
	}
	if initialCalls != 1 {
		t.Fatalf("initial readiness calls=%d, want 1", initialCalls)
	}
	if err := r.options.ConnectedHealth(context.Background(), v1.SessionRef{Generation: 1}); err != nil {
		t.Fatalf("first monitored check failed: %v", err)
	}
	if err := r.options.ConnectedHealth(context.Background(), v1.SessionRef{Generation: 1}); err == nil {
		t.Fatal("second monitored check unexpectedly succeeded")
	}
	if healthCalls != 2 {
		t.Fatalf("explicit health fault seam calls=%d, want 2", healthCalls)
	}
	if r.options.HealthInterval != time.Second || r.options.HealthFailureThreshold != 1 {
		t.Fatalf("explicit fault timing=%s threshold=%d, want 1s/1", r.options.HealthInterval, r.options.HealthFailureThreshold)
	}
}

func TestConnectedHealthMonitorStopsWithRuntimeLease(t *testing.T) {
	record := &recorded{}
	entered := make(chan struct{}, 1)
	canceled := make(chan struct{}, 1)
	o := options(record)
	o.ConnectedHealth = func(ctx context.Context, _ v1.SessionRef) error {
		entered <- struct{}{}
		<-ctx.Done()
		canceled <- struct{}{}
		return ctx.Err()
	}
	lease, err := New(o).Start(context.Background(), v1.SessionRef{Generation: 1}, profile())
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	if err := lease.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("health monitor was not canceled with the runtime lease")
	}
}

func TestStartUsesNormalizedConfigAndStopsLIFOIdempotently(t *testing.T) {
	record := &recorded{}
	r := New(options(record))
	lease, err := r.Start(context.Background(), v1.SessionRef{SessionID: "s", Generation: 1}, profile())
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := lease.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{"inputs", "device", "connect", "core-stop", "inputs-stop"}
	if got := record.got(); !same(got, want) {
		t.Fatalf("order=%v, want %v", got, want)
	}
}

func TestFailureRollsBackEachAcquiredResource(t *testing.T) {
	cases := []struct {
		name string
		edit func(*Options)
		want []string
	}{
		{"inputs", func(o *Options) { o.Inputs = fakeInputs{record: &recorded{}, err: errors.New("inputs")} }, nil},
		{"device", func(o *Options) {
			o.NewDevice = func(context.Context, v1.SessionRef, v1.RuntimeProfile, SocketProtector) (protocol.ProtocolDevice, error) {
				return nil, errors.New("device")
			}
		}, []string{"inputs", "inputs-stop"}},
		{"core", func(*Options) {}, []string{"inputs", "device", "connect", "core-stop", "inputs-stop"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			record := &recorded{}
			o := options(record)
			if tc.name == "inputs" {
				o.Inputs = fakeInputs{record: record, err: errors.New("inputs")}
			} else {
				tc.edit(&o)
			}
			if tc.name == "core" {
				o.NewCore = func(protocol.ProtocolDevice, io.ReadWriteCloser) sessionCore {
					return fakeCore{record: record, connectErr: errors.New("core")}
				}
			}
			if _, err := New(o).Start(context.Background(), v1.SessionRef{}, profile()); err == nil {
				t.Fatal("Start succeeded")
			}
			if got := record.got(); !same(got, tc.want) {
				t.Fatalf("order=%v, want=%v", got, tc.want)
			}
		})
	}
}

func TestProtectionFailureIsFatal(t *testing.T) {
	// On desktop the injected factory still receives the correlated protector;
	// mobile factories use the same callback for actual protocol sockets.
	record := &recorded{}
	o := options(record)
	o.Tunnel = protectionProvider{err: errors.New("denied")}
	o.NewDevice = func(ctx context.Context, ref v1.SessionRef, _ v1.RuntimeProfile, protect SocketProtector) (protocol.ProtocolDevice, error) {
		return nil, protect(ctx, 41)
	}
	if _, err := New(o).Start(context.Background(), v1.SessionRef{Generation: 9}, profile()); err == nil {
		t.Fatal("Start succeeded after protection failure")
	}
	if got := record.got(); !same(got, []string{"inputs", "inputs-stop"}) {
		t.Fatalf("order=%v", got)
	}
}

type protectionProvider struct{ err error }

func (p protectionProvider) Acquire(context.Context, v1.SessionRef) (TunnelLease, error) {
	return nil, errors.New("unexpected")
}
func (p protectionProvider) ProtectSocket(context.Context, v1.SessionRef, int) error { return p.err }

func TestProbeOwnsTemporaryResourcesAndRuntimeDoesNotOverlap(t *testing.T) {
	record := &recorded{}
	o := options(record)
	readinessChecks := 0
	o.InitialReadiness = func(context.Context, v1.SessionRef) error {
		readinessChecks++
		return nil
	}
	r := New(o)
	result, err := r.Probe(context.Background(), v1.SessionRef{Generation: 1}, profile())
	if err != nil || result.LatencyMillis != 7 {
		t.Fatalf("Probe=%#v err=%v", result, err)
	}
	if readinessChecks != 0 {
		t.Fatalf("Probe ran final-session readiness checks=%d", readinessChecks)
	}
	want := []string{"inputs", "device", "connect", "probe", "core-stop", "inputs-stop"}
	if got := record.got(); !same(got, want) {
		t.Fatalf("probe order=%v", got)
	}

	lease, err := r.Start(context.Background(), v1.SessionRef{Generation: 2}, profile())
	if err != nil {
		t.Fatal(err)
	}
	if _, overlapErr := r.Start(context.Background(), v1.SessionRef{Generation: 3}, profile()); overlapErr == nil {
		t.Fatal("overlapping Start succeeded")
	}
	_ = lease.Stop(context.Background())
	if lease, err = r.Start(context.Background(), v1.SessionRef{Generation: 4}, profile()); err != nil {
		t.Fatal(err)
	}
	_ = lease.Stop(context.Background())
}

func TestProbeRetriesTransientReadinessFailureWithinOneLease(t *testing.T) {
	record := &recorded{}
	o := options(record)
	o.ReadinessAttempts = 3
	o.ReadinessRetryInterval = time.Nanosecond
	attempts := 0
	o.Probe = func(context.Context) (int64, error) {
		attempts++
		record.add("probe")
		if attempts < 3 {
			return -1, nil
		}
		return 11, nil
	}

	result, err := New(o).Probe(context.Background(), v1.SessionRef{Generation: 7}, profile())
	if err != nil || result.LatencyMillis != 11 {
		t.Fatalf("Probe=%#v err=%v", result, err)
	}
	if attempts != 3 {
		t.Fatalf("probe attempts=%d, want 3", attempts)
	}
	want := []string{"inputs", "device", "connect", "probe", "probe", "probe", "core-stop", "inputs-stop"}
	if got := record.got(); !same(got, want) {
		t.Fatalf("probe order=%v, want=%v", got, want)
	}
}

func TestProbeExhaustsReadinessRetriesAndCleansUp(t *testing.T) {
	record := &recorded{}
	o := options(record)
	o.ReadinessAttempts = 2
	o.ReadinessRetryInterval = time.Nanosecond
	o.Probe = func(context.Context) (int64, error) {
		record.add("probe")
		return -1, nil
	}

	result, err := New(o).Probe(context.Background(), v1.SessionRef{Generation: 8}, profile())
	if err == nil || err.Error() != "runtime health probe did not reach quorum" {
		t.Fatalf("Probe error=%v", err)
	}
	if result != (v1.ProbeResult{}) {
		t.Fatalf("Probe result=%#v, want empty result", result)
	}
	want := []string{"inputs", "device", "connect", "probe", "probe", "core-stop", "inputs-stop"}
	if got := record.got(); !same(got, want) {
		t.Fatalf("probe order=%v, want=%v", got, want)
	}
}

func TestProbeReturnsRealErrorWithoutRetry(t *testing.T) {
	record := &recorded{}
	o := options(record)
	o.ReadinessAttempts = 3
	want := errors.New("probe transport failed")
	o.Probe = func(context.Context) (int64, error) {
		record.add("probe")
		return 0, want
	}

	_, err := New(o).Probe(context.Background(), v1.SessionRef{Generation: 9}, profile())
	if !errors.Is(err, want) {
		t.Fatalf("Probe error=%v, want %v", err, want)
	}
	wantOrder := []string{"inputs", "device", "connect", "probe", "core-stop", "inputs-stop"}
	if got := record.got(); !same(got, wantOrder) {
		t.Fatalf("probe order=%v, want=%v", got, wantOrder)
	}
}

func TestProbeCancellationDuringRetryWaitCleansUp(t *testing.T) {
	record := &recorded{}
	o := options(record)
	o.ReadinessAttempts = 3
	o.ReadinessRetryInterval = time.Hour
	first := make(chan struct{})
	o.Probe = func(context.Context) (int64, error) {
		record.add("probe")
		close(first)
		return -1, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := New(o).Probe(ctx, v1.SessionRef{Generation: 10}, profile())
		done <- err
	}()
	<-first
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Probe error=%v, want cancellation", err)
	}
	want := []string{"inputs", "device", "connect", "probe", "core-stop", "inputs-stop"}
	if got := record.got(); !same(got, want) {
		t.Fatalf("probe order=%v, want=%v", got, want)
	}
}

func TestProbeDeadlineDuringRetryWaitCleansUp(t *testing.T) {
	record := &recorded{}
	o := options(record)
	o.ProbeTimeout = time.Millisecond
	o.ReadinessAttempts = 3
	o.ReadinessRetryInterval = time.Hour
	o.Probe = func(context.Context) (int64, error) {
		record.add("probe")
		return -1, nil
	}

	_, err := New(o).Probe(context.Background(), v1.SessionRef{Generation: 11}, profile())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Probe error=%v, want deadline", err)
	}
	want := []string{"inputs", "device", "connect", "probe", "core-stop", "inputs-stop"}
	if got := record.got(); !same(got, want) {
		t.Fatalf("probe order=%v, want=%v", got, want)
	}
}

func TestProbeRejectsLatePositiveResultAfterDeadline(t *testing.T) {
	record := &recorded{}
	o := options(record)
	o.ProbeTimeout = time.Millisecond
	o.Probe = func(ctx context.Context) (int64, error) {
		record.add("probe")
		<-ctx.Done()
		return 7, nil
	}

	_, err := New(o).Probe(context.Background(), v1.SessionRef{Generation: 12}, profile())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Probe error=%v, want deadline", err)
	}
	want := []string{"inputs", "device", "connect", "probe", "core-stop", "inputs-stop"}
	if got := record.got(); !same(got, want) {
		t.Fatalf("probe order=%v, want=%v", got, want)
	}
}

func TestProbeReportsCleanupFailure(t *testing.T) {
	record := &recorded{}
	want := errors.New("probe cleanup failed")
	o := options(record)
	o.ReadinessAttempts = 2
	o.ReadinessRetryInterval = time.Nanosecond
	attempts := 0
	o.Probe = func(context.Context) (int64, error) {
		attempts++
		if attempts == 1 {
			return -1, nil
		}
		return 7, nil
	}
	o.NewCore = func(protocol.ProtocolDevice, io.ReadWriteCloser) sessionCore {
		return fakeCore{record: record, disconnectErr: want}
	}
	result, err := New(o).Probe(context.Background(), v1.SessionRef{Generation: 1}, profile())
	if !errors.Is(err, want) {
		t.Fatalf("Probe error=%v, want %v", err, want)
	}
	if attempts != 2 {
		t.Fatalf("probe attempts=%d, want 2", attempts)
	}
	if result != (v1.ProbeResult{}) {
		t.Fatalf("Probe result=%#v, want empty result after cleanup failure", result)
	}
}

func TestRuntimeRejectsStartUntilPriorCleanupFinishes(t *testing.T) {
	record := &recorded{}
	block := make(chan struct{})
	o := options(record)
	o.NewCore = func(protocol.ProtocolDevice, io.ReadWriteCloser) sessionCore {
		return fakeCore{record: record, stopBlock: block}
	}
	r := New(o)
	lease, err := r.Start(context.Background(), v1.SessionRef{Generation: 1}, profile())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- lease.Stop(context.Background()) }()
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
		if got := record.got(); len(got) > 0 && got[len(got)-1] == "core-stop" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := r.Start(context.Background(), v1.SessionRef{Generation: 2}, profile()); err == nil {
		t.Fatal("Start succeeded while previous cleanup was blocked")
	}
	second := make(chan error, 1)
	go func() { second <- lease.Stop(context.Background()) }()
	select {
	case err := <-second:
		t.Fatalf("second Stop returned before cleanup completed: %v", err)
	case <-time.After(10 * time.Millisecond):
	}
	close(block)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-second; err != nil {
		t.Fatal(err)
	}
}

func TestProbeCancellationIsDeterministicAndCleansUp(t *testing.T) {
	record := &recorded{}
	o := options(record)
	o.Probe = func(ctx context.Context) (int64, error) { <-ctx.Done(); return 0, ctx.Err() }
	o.ProbeTimeout = time.Millisecond
	_, err := New(o).Probe(context.Background(), v1.SessionRef{}, profile())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v", err)
	}
	got := record.got()
	if len(got) < 2 || got[len(got)-2] != "core-stop" || got[len(got)-1] != "inputs-stop" {
		t.Fatalf("cleanup order=%v", got)
	}
}

func TestProbeTimeoutUsesContextDeadline(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(100*time.Millisecond))
	defer cancel()
	timeout, err := probeTimeout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if timeout <= 0 || timeout > 100*time.Millisecond {
		t.Fatalf("timeout=%s, want remaining context deadline", timeout)
	}
}

func TestProbeTimeoutRejectsExpiredContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := probeTimeout(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v, want context cancellation", err)
	}
}

func same(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
