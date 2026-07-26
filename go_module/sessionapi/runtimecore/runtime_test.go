package runtimecore

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"go_module/core/pkg"
	v1 "go_module/sessionapi/v1"
)

func profile() v1.RuntimeProfile {
	return v1.RuntimeProfile{
		Summary:          v1.ProfileSummary{Protocol: v1.ProtocolOutline},
		RawTOML:          []byte("Cloak = true\nRemoteHost = 'example.invalid'\n"),
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
	record      *recorded
	connectErr  error
	block       <-chan struct{}
	stopBlock   <-chan struct{}
	stopEntered chan<- struct{}
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
	return nil
}

type fakeDevice struct{}

func (fakeDevice) Open(int, string) error { return nil }
func (fakeDevice) GetProxyAddr() string   { return "127.0.0.1:1" }
func (fakeDevice) GetServerIP() net.IP    { return nil }
func (fakeDevice) Close() error           { return nil }

func options(record *recorded) Options {
	return Options{
		Inputs: fakeInputs{record: record},
		StartCloak: func(_ context.Context, _ v1.SessionRef, raw []byte) (func(context.Context) error, error) {
			if string(raw) == "normalized-only" {
				return nil, errors.New("Cloak received normalized config")
			}
			record.add("cloak")
			return func(context.Context) error { record.add("cloak-stop"); return nil }, nil
		},
		NewDevice: func(_ context.Context, _ v1.SessionRef, got v1.RuntimeProfile, _ SocketProtector) (pkg.ProtocolDevice, error) {
			if string(got.NormalizedConfig) != "normalized-only" {
				return nil, errors.New("device did not receive normalized config")
			}
			record.add("device")
			return fakeDevice{}, nil
		},
		NewCore: func(pkg.ProtocolDevice, io.ReadWriteCloser) coreClient {
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
	want := []string{"inputs", "cloak", "device", "connect", "ready", "ready", "ready", "core-stop", "cloak-stop", "inputs-stop"}
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
	want := []string{"inputs", "cloak", "device", "connect", "ready", "ready", "core-stop", "cloak-stop", "inputs-stop"}
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
	want := []string{"inputs", "cloak", "device", "connect", "core-stop", "cloak-stop", "inputs-stop"}
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
	want := []string{"inputs", "cloak", "device", "connect", "core-stop", "cloak-stop", "inputs-stop"}
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
	o.NewCore = func(pkg.ProtocolDevice, io.ReadWriteCloser) coreClient {
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
	want := []string{"inputs", "cloak", "device", "connect", "core-stop", "cloak-stop", "inputs-stop"}
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
		{"cloak", func(o *Options) {
			o.StartCloak = func(context.Context, v1.SessionRef, []byte) (func(context.Context) error, error) {
				return nil, errors.New("cloak")
			}
		}, []string{"inputs", "inputs-stop"}},
		{"device", func(o *Options) {
			o.NewDevice = func(context.Context, v1.SessionRef, v1.RuntimeProfile, SocketProtector) (pkg.ProtocolDevice, error) {
				return nil, errors.New("device")
			}
		}, []string{"inputs", "cloak", "cloak-stop", "inputs-stop"}},
		{"core", func(*Options) {}, []string{"inputs", "cloak", "device", "connect", "core-stop", "cloak-stop", "inputs-stop"}},
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
				o.NewCore = func(pkg.ProtocolDevice, io.ReadWriteCloser) coreClient {
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
	o.NewDevice = func(ctx context.Context, ref v1.SessionRef, _ v1.RuntimeProfile, protect SocketProtector) (pkg.ProtocolDevice, error) {
		return nil, protect(ctx, 41)
	}
	if _, err := New(o).Start(context.Background(), v1.SessionRef{Generation: 9}, profile()); err == nil {
		t.Fatal("Start succeeded after protection failure")
	}
	if got := record.got(); !same(got, []string{"inputs", "cloak", "cloak-stop", "inputs-stop"}) {
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
	want := []string{"inputs", "cloak", "device", "connect", "probe", "core-stop", "cloak-stop", "inputs-stop"}
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

func TestRuntimeRejectsStartUntilPriorCleanupFinishes(t *testing.T) {
	record := &recorded{}
	block := make(chan struct{})
	o := options(record)
	o.NewCore = func(pkg.ProtocolDevice, io.ReadWriteCloser) coreClient {
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
	if len(got) < 3 || got[len(got)-3] != "core-stop" || got[len(got)-2] != "cloak-stop" || got[len(got)-1] != "inputs-stop" {
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

func TestCloakTOMLDefaultsAndExcludesOutlineSecrets(t *testing.T) {
	raw := []byte(`Cloak = true
Server = "edge.invalid"
Port = 444
Password = "must-not-cross"
EncryptionMethod = "aes-256-gcm"
UID = "dWlk"
PublicKey = "cHVia2V5"
CDNWsUrlPath = "/ws"`)
	encoded, err := NormalizeCloakProfile(raw)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]any{
		"Transport": "CDN", "ProxyMethod": "shadowsocks", "NumConn": float64(8), "StreamTimeout": float64(300),
		"RemoteHost": "edge.invalid", "RemotePort": "444", "ServerName": "edge.invalid", "CDNOriginHost": "edge.invalid",
	} {
		if got[key] != want {
			t.Fatalf("%s=%#v, want %#v", key, got[key], want)
		}
	}
	if _, leaked := got["Password"]; leaked {
		t.Fatalf("Cloak JSON leaked Outline password: %s", encoded)
	}
}

func TestCloakTOMLDirectOmitsCDNFieldsAndRejectsMissingRequired(t *testing.T) {
	raw := []byte(`Cloak = true
Server = "edge.invalid"
EncryptionMethod = "plain"
UID = "dWlk"
PublicKey = "cHVia2V5"`)
	encoded, err := NormalizeCloakProfile(raw)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if got["Transport"] != "direct" {
		t.Fatalf("transport=%#v", got["Transport"])
	}
	if _, ok := got["CDNOriginHost"]; ok {
		t.Fatalf("direct config has CDNOriginHost: %s", encoded)
	}
	if _, err := NormalizeCloakProfile([]byte("Cloak = true\nServer = 'edge.invalid'")); err == nil {
		t.Fatal("missing required Cloak fields accepted")
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
