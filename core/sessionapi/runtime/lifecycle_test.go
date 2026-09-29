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

	"core/dnscache"
	"core/protocol"
	"core/sessionapi"
	"core/tunnel"
)

func profile() sessionapi.RuntimeProfile {
	return sessionapi.RuntimeProfile{
		Summary:          sessionapi.ProfileSummary{Protocol: sessionapi.ProtocolOutline},
		NormalizedConfig: []byte("normalized-only"),
		ExcludeCIDRs:     []string{"203.0.113.0/24"},
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

func (f fakeInputs) Resolve(ctx context.Context, cidrs []string) (*tunnel.BypassPolicy, error) {
	if f.err != nil {
		return nil, f.err
	}
	if len(cidrs) != 1 {
		return nil, errors.New("runtime did not supply Go-only inputs")
	}
	f.record.add("inputs")
	return tunnel.ResolveBypassPolicy(ctx, cidrs)
}

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

type cancelableBlockingCore struct {
	record        *recorded
	started       chan struct{}
	release       chan struct{}
	connectDone   chan struct{}
	cancelOnce    sync.Once
	startOnce     sync.Once
	cancelRequest chan struct{}
}

func (c *cancelableBlockingCore) Connect() error {
	c.record.add("connect")
	c.startOnce.Do(func() { close(c.started) })
	<-c.release
	close(c.connectDone)
	return errors.New("test startup released after cancellation")
}

func (c *cancelableBlockingCore) CancelConnect() {
	c.record.add("connect-cancel")
	c.cancelOnce.Do(func() { close(c.cancelRequest) })
}

func (c *cancelableBlockingCore) Disconnect() error {
	<-c.connectDone
	c.record.add("core-stop")
	return nil
}

type fakeDevice struct{}

func (fakeDevice) Open(int, string) error { return nil }
func (fakeDevice) GetProxyAddr() string   { return "127.0.0.1:1" }
func (fakeDevice) GetServerIP() net.IP    { return nil }
func (fakeDevice) Close() error           { return nil }

type closingDevice struct {
	fakeDevice
	closes int
	err    error
}

func (d *closingDevice) Close() error {
	d.closes++
	return d.err
}

func TestDeviceIsClosedWhenCoreConstructionFails(t *testing.T) {
	record := &recorded{}
	device := &closingDevice{}
	o := options(record)
	o.NewDevice = func(context.Context, sessionapi.SessionRef, sessionapi.RuntimeProfile, SocketProtector, *dnscache.Cache) (protocol.ProtocolDevice, error) {
		return device, nil
	}
	o.NewCore = func(protocol.ProtocolDevice, io.ReadWriteCloser, *dnscache.Cache, *tunnel.BypassPolicy) sessionCore {
		return nil
	}
	if _, err := New(o).Start(context.Background(), sessionapi.SessionRef{Generation: 1}, profile()); err == nil {
		t.Fatal("missing core construction failure")
	}
	if device.closes != 1 {
		t.Fatalf("device close calls=%d, want 1", device.closes)
	}
}

func options(record *recorded) Options {
	return Options{
		Inputs: fakeInputs{record: record},
		NewDevice: func(_ context.Context, _ sessionapi.SessionRef, got sessionapi.RuntimeProfile, _ SocketProtector, _ *dnscache.Cache) (protocol.ProtocolDevice, error) {
			if string(got.NormalizedConfig) != "normalized-only" {
				return nil, errors.New("device did not receive normalized config")
			}
			record.add("device")
			return fakeDevice{}, nil
		},
		NewCore: func(protocol.ProtocolDevice, io.ReadWriteCloser, *dnscache.Cache, *tunnel.BypassPolicy) sessionCore {
			return fakeCore{record: record}
		},
		InitialReadiness: func(context.Context, sessionapi.SessionRef, string) error {
			return nil
		},
		ConnectedHealth: func(ctx context.Context, _ sessionapi.SessionRef, _ string) error {
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
	o.InitialReadiness = func(context.Context, sessionapi.SessionRef, string) error {
		attempts++
		record.add("ready")
		if attempts < 3 {
			return errors.New("not ready")
		}
		return nil
	}

	lease, err := New(o).Start(context.Background(), sessionapi.SessionRef{Generation: 1}, profile())
	if err != nil {
		t.Fatal(err)
	}
	if attempts != 3 {
		t.Fatalf("readiness attempts=%d, want 3", attempts)
	}
	if err := lease.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{"inputs", "device", "connect", "ready", "ready", "ready", "core-stop"}
	if got := record.got(); !same(got, want) {
		t.Fatalf("order=%v, want=%v", got, want)
	}
}

func TestInitialReadinessFailureRollsBackLIFO(t *testing.T) {
	record := &recorded{}
	o := options(record)
	o.ReadinessAttempts = 2
	o.ReadinessRetryInterval = time.Nanosecond
	attempts := 0
	o.InitialReadiness = func(context.Context, sessionapi.SessionRef, string) error {
		attempts++
		record.add("ready")
		if attempts == 1 {
			return errors.New("socket unavailable")
		}
		return errors.New("response timed out")
	}

	if _, err := New(o).Start(context.Background(), sessionapi.SessionRef{Generation: 2}, profile()); err == nil {
		t.Fatal("Start succeeded without tunnel readiness")
	} else {
		for _, want := range []string{"socket unavailable", "response timed out"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("readiness failure lost %q: %v", want, err)
			}
		}
	}
	want := []string{"inputs", "device", "connect", "ready", "ready", "core-stop"}
	if got := record.got(); !same(got, want) {
		t.Fatalf("order=%v, want=%v", got, want)
	}
}

func TestInitialReadinessCancellationRollsBackLIFO(t *testing.T) {
	record := &recorded{}
	o := options(record)
	o.ReadinessAttempts = 2
	o.ReadinessRetryInterval = time.Nanosecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	attempts := 0
	o.InitialReadiness = func(_ context.Context, _ sessionapi.SessionRef, _ string) error {
		attempts++
		record.add("ready")
		if attempts == 1 {
			return errors.New("first readiness detail")
		}
		cancel()
		return context.Canceled
	}
	result := make(chan error, 1)
	go func() {
		_, err := New(o).Start(ctx, sessionapi.SessionRef{Generation: 3}, profile())
		result <- err
	}()
	err := <-result
	if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "first readiness detail") {
		t.Fatalf("err=%v, want cancellation and prior readiness detail", err)
	}
	want := []string{"inputs", "device", "connect", "ready", "ready", "core-stop"}
	if got := record.got(); !same(got, want) {
		t.Fatalf("order=%v, want=%v", got, want)
	}
}

func TestStartCancellationTransfersLeaseUntilNativeConnectReturns(t *testing.T) {
	record := &recorded{}
	core := &cancelableBlockingCore{
		record:        record,
		started:       make(chan struct{}),
		release:       make(chan struct{}),
		connectDone:   make(chan struct{}),
		cancelRequest: make(chan struct{}),
	}
	o := options(record)
	o.NewCore = func(protocol.ProtocolDevice, io.ReadWriteCloser, *dnscache.Cache, *tunnel.BypassPolicy) sessionCore {
		return core
	}
	r := New(o).(*runtime)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type startResult struct {
		lease sessionapi.RuntimeLease
		err   error
	}
	started := make(chan startResult, 1)
	go func() {
		lease, err := r.Start(ctx, sessionapi.SessionRef{Generation: 30}, profile())
		started <- startResult{lease: lease, err: err}
	}()
	<-core.started
	cancel()

	var result startResult
	select {
	case result = <-started:
	case <-time.After(time.Second):
		t.Fatal("canceled Start did not return ownership promptly")
	}
	if !errors.Is(result.err, context.Canceled) {
		t.Fatalf("Start error=%v, want cancellation", result.err)
	}
	if result.lease == nil {
		t.Fatal("canceled Start dropped the still-active runtime lease")
	}
	select {
	case <-core.cancelRequest:
	default:
		t.Fatal("canceled Start did not request native connect cancellation")
	}
	r.mu.Lock()
	active := r.active
	r.mu.Unlock()
	if !active {
		t.Fatal("runtime became reusable before the native startup lease was cleaned")
	}

	stopDone := make(chan error, 1)
	go func() { stopDone <- result.lease.Stop(context.Background()) }()
	select {
	case err := <-stopDone:
		t.Fatalf("lease cleanup returned while Connect was blocked: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	if got := record.got(); same(got, []string{"inputs", "device", "connect", "connect-cancel"}) == false {
		t.Fatalf("cleanup mutated resources while Connect was blocked: %v", got)
	}

	close(core.release)
	if err := <-stopDone; err != nil {
		t.Fatalf("transferred lease cleanup failed: %v", err)
	}
	r.mu.Lock()
	active = r.active
	r.mu.Unlock()
	if active {
		t.Fatal("runtime remained active after transferred lease cleanup")
	}
	want := []string{"inputs", "device", "connect", "connect-cancel", "core-stop"}
	if got := record.got(); !same(got, want) {
		t.Fatalf("cleanup order=%v, want=%v", got, want)
	}
}

func TestInitialReadinessAttemptTimeoutIsBounded(t *testing.T) {
	record := &recorded{}
	o := options(record)
	o.ReadinessAttempts = 1
	o.ReadinessAttemptTimeout = time.Millisecond
	o.InitialReadiness = func(ctx context.Context, _ sessionapi.SessionRef, _ string) error {
		<-ctx.Done()
		return ctx.Err()
	}
	started := time.Now()
	_, err := New(o).Start(context.Background(), sessionapi.SessionRef{Generation: 4}, profile())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v, want readiness deadline", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("readiness timeout took %s", elapsed)
	}
	want := []string{"inputs", "device", "connect", "core-stop"}
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
	o.InitialReadiness = func(_ context.Context, ref sessionapi.SessionRef, _ string) error {
		if ref.Generation == 1 {
			return errors.New("not ready")
		}
		return nil
	}
	o.NewCore = func(protocol.ProtocolDevice, io.ReadWriteCloser, *dnscache.Cache, *tunnel.BypassPolicy) sessionCore {
		return fakeCore{
			record:      record,
			stopBlock:   cleanupRelease,
			stopEntered: cleanupEntered,
		}
	}
	r := New(o)

	first := make(chan error, 1)
	go func() {
		_, err := r.Start(context.Background(), sessionapi.SessionRef{Generation: 1}, profile())
		first <- err
	}()
	<-cleanupEntered

	second := make(chan error, 1)
	var secondLease sessionapi.RuntimeLease
	go func() {
		lease, err := r.Start(context.Background(), sessionapi.SessionRef{Generation: 2}, profile())
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
	o.ConnectedHealth = func(ctx context.Context, _ sessionapi.SessionRef, _ string) error {
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

	lease, err := New(o).Start(context.Background(), sessionapi.SessionRef{Generation: 1}, profile())
	if err != nil {
		t.Fatal(err)
	}
	monitored, ok := lease.(sessionapi.HealthMonitoringLease)
	if !ok {
		t.Fatal("runtime lease does not expose connected health monitoring")
	}
	<-entered
	checks <- errors.New("first failed check")
	<-entered
	checks <- errors.New("second failed check")
	select {
	case cause := <-monitored.HealthFailures():
		for _, want := range []string{"first failed check", "second failed check"} {
			if cause == nil || !strings.Contains(cause.Error(), want) {
				t.Fatalf("health failure cause=%v, missing %q", cause, want)
			}
		}
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
	o.ConnectedHealth = func(ctx context.Context, _ sessionapi.SessionRef, _ string) error {
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

	lease, err := New(o).Start(context.Background(), sessionapi.SessionRef{Generation: 1}, profile())
	if err != nil {
		t.Fatal(err)
	}
	monitored := lease.(sessionapi.HealthMonitoringLease)
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
	o.ConnectedHealth = func(context.Context, sessionapi.SessionRef, string) error {
		checks++
		return nil
	}
	r := New(o).(*runtime)
	if r.options.HealthInterval != 10*time.Second || r.options.HealthFailureThreshold != 3 {
		t.Fatalf("legacy environment changed product defaults: interval=%s threshold=%d", r.options.HealthInterval, r.options.HealthFailureThreshold)
	}
	if err := r.options.ConnectedHealth(context.Background(), sessionapi.SessionRef{Generation: 1}, ""); err != nil {
		t.Fatal(err)
	}
	if checks != 1 {
		t.Fatalf("custom health seam calls=%d, want 1", checks)
	}
}

func TestExplicitHealthFaultSeamLeavesInitialReadinessUntouched(t *testing.T) {
	o := options(&recorded{})
	initialCalls := 0
	o.InitialReadiness = func(context.Context, sessionapi.SessionRef, string) error {
		initialCalls++
		return nil
	}
	healthCalls := 0
	o.ConnectedHealth = func(context.Context, sessionapi.SessionRef, string) error {
		healthCalls++
		if healthCalls > 1 {
			return errors.New("test health fault after 1 successful check")
		}
		return nil
	}
	o.HealthInterval = time.Second
	o.HealthFailureThreshold = 1
	r := New(o).(*runtime)

	if err := r.options.InitialReadiness(context.Background(), sessionapi.SessionRef{Generation: 1}, ""); err != nil {
		t.Fatalf("initial readiness was faulted: %v", err)
	}
	if initialCalls != 1 {
		t.Fatalf("initial readiness calls=%d, want 1", initialCalls)
	}
	if err := r.options.ConnectedHealth(context.Background(), sessionapi.SessionRef{Generation: 1}, ""); err != nil {
		t.Fatalf("first monitored check failed: %v", err)
	}
	if err := r.options.ConnectedHealth(context.Background(), sessionapi.SessionRef{Generation: 1}, ""); err == nil {
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
	o.ConnectedHealth = func(ctx context.Context, _ sessionapi.SessionRef, _ string) error {
		entered <- struct{}{}
		<-ctx.Done()
		canceled <- struct{}{}
		return ctx.Err()
	}
	lease, err := New(o).Start(context.Background(), sessionapi.SessionRef{Generation: 1}, profile())
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
	lease, err := r.Start(context.Background(), sessionapi.SessionRef{SessionID: "s", Generation: 1}, profile())
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := lease.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{"inputs", "device", "connect", "core-stop"}
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
			o.NewDevice = func(context.Context, sessionapi.SessionRef, sessionapi.RuntimeProfile, SocketProtector, *dnscache.Cache) (protocol.ProtocolDevice, error) {
				return nil, errors.New("device")
			}
		}, []string{"inputs"}},
		{"core", func(*Options) {}, []string{"inputs", "device", "connect", "core-stop"}},
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
				o.NewCore = func(protocol.ProtocolDevice, io.ReadWriteCloser, *dnscache.Cache, *tunnel.BypassPolicy) sessionCore {
					return fakeCore{record: record, connectErr: errors.New("core")}
				}
			}
			if _, err := New(o).Start(context.Background(), sessionapi.SessionRef{}, profile()); err == nil {
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
	o.NewDevice = func(ctx context.Context, ref sessionapi.SessionRef, _ sessionapi.RuntimeProfile, protect SocketProtector, _ *dnscache.Cache) (protocol.ProtocolDevice, error) {
		return nil, protect(ctx, 41)
	}
	if _, err := New(o).Start(context.Background(), sessionapi.SessionRef{Generation: 9}, profile()); err == nil {
		t.Fatal("Start succeeded after protection failure")
	}
	if got := record.got(); !same(got, []string{"inputs"}) {
		t.Fatalf("order=%v", got)
	}
}

type protectionProvider struct{ err error }

func (p protectionProvider) Acquire(context.Context, sessionapi.SessionRef) (TunnelLease, error) {
	return nil, errors.New("unexpected")
}
func (p protectionProvider) ProtectSocket(context.Context, sessionapi.SessionRef, int) error {
	return p.err
}

func TestRuntimeRejectsStartUntilPriorCleanupFinishes(t *testing.T) {
	record := &recorded{}
	block := make(chan struct{})
	o := options(record)
	o.NewCore = func(protocol.ProtocolDevice, io.ReadWriteCloser, *dnscache.Cache, *tunnel.BypassPolicy) sessionCore {
		return fakeCore{record: record, stopBlock: block}
	}
	r := New(o)
	lease, err := r.Start(context.Background(), sessionapi.SessionRef{Generation: 1}, profile())
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
	if _, err := r.Start(context.Background(), sessionapi.SessionRef{Generation: 2}, profile()); err == nil {
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
