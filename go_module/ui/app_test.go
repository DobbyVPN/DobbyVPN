package ui

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/mobile"
	"fyne.io/fyne/v2/test"
)

type fakeClient struct {
	mu            sync.Mutex
	snapshot      Snapshot
	configure     ConfigureResult
	configuredRaw []byte
	startGate     <-chan struct{}
	stopGate      <-chan struct{}
	started       bool
	stopped       bool
	startedCh     chan struct{}
}

type reconnectClient struct {
	mu      sync.Mutex
	watches int
	updates chan Snapshot
}

type fakeLogExporter struct {
	mu    sync.Mutex
	lines []string
}

// startupLifecycleApp lets the startup contract test model the one important
// property of a native Fyne driver: its started callback is delivered only
// after Run has entered the driver loop. The underlying test app still supplies
// the in-memory window and software driver.
type startupLifecycleApp struct {
	fyne.App
	lifecycle   *startupLifecycle
	runEntered  chan struct{}
	beforeStart func()
}

type startupTrackingWindow struct {
	fyne.Window
	events     *[]string
	beforeShow func()
}

func (w *startupTrackingWindow) Show() {
	if w.beforeShow != nil {
		w.beforeShow()
	}
	*w.events = append(*w.events, "show")
	w.Window.Show()
}

type startupLifecycle struct {
	onStarted    func()
	onStopped    func()
	onForeground func()
	onBackground func()
}

func (l *startupLifecycle) SetOnEnteredForeground(fn func()) { l.onForeground = fn }
func (l *startupLifecycle) SetOnExitedForeground(fn func())  { l.onBackground = fn }
func (l *startupLifecycle) SetOnStarted(fn func())           { l.onStarted = fn }
func (l *startupLifecycle) SetOnStopped(fn func())           { l.onStopped = fn }

func (a *startupLifecycleApp) Lifecycle() fyne.Lifecycle { return a.lifecycle }

func (a *startupLifecycleApp) Run() {
	close(a.runEntered)
	if a.beforeStart != nil {
		a.beforeStart()
	}
	if a.lifecycle.onStarted != nil {
		a.lifecycle.onStarted()
	}
}

func (f *fakeLogExporter) Export(_ context.Context, lines []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lines = append([]string(nil), lines...)
	return nil
}

func (f *fakeLogExporter) captured() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.lines...)
}

func TestApplicationDefersNativeDiagnosticResolutionUntilDriverStarted(t *testing.T) {
	base := test.NewApp()
	runtime := &startupLifecycleApp{
		App:        base,
		lifecycle:  &startupLifecycle{},
		runEntered: make(chan struct{}),
	}
	application := NewApplication(runtime, &fakeClient{})
	t.Cleanup(application.Close)
	events := []string{}
	application.Window = &startupTrackingWindow{Window: application.Window, events: &events}
	startedBeforeLifecycle := make(chan bool, 1)
	runtime.beforeStart = func() {
		application.Connection.mu.Lock()
		started := application.Connection.started
		application.Connection.mu.Unlock()
		startedBeforeLifecycle <- started
	}

	resolved := make(chan bool, 1)
	application.RunWithDiagnosticStore(func() DiagnosticStore {
		application.Connection.mu.Lock()
		started := application.Connection.started
		application.Connection.mu.Unlock()
		select {
		case <-runtime.runEntered:
			resolved <- started && len(events) == 1 && events[0] == "show"
		default:
			resolved <- false
		}
		return nil
	})
	if started := <-startedBeforeLifecycle; started {
		t.Fatal("mobile session watcher started before the Fyne driver lifecycle")
	}

	select {
	case started := <-resolved:
		if !started {
			t.Fatal("native diagnostic store resolved before the Fyne driver started")
		}
	case <-time.After(time.Second):
		t.Fatal("native diagnostic store was not resolved")
	}
}

func TestApplicationDefersDesktopStartUntilDriverStarted(t *testing.T) {
	base := test.NewApp()
	runtime := &startupLifecycleApp{
		App:        base,
		lifecycle:  &startupLifecycle{},
		runEntered: make(chan struct{}),
	}
	client := &fakeClient{}
	application := NewApplication(runtime, client)
	t.Cleanup(application.Close)
	startedBeforeLifecycle := make(chan bool, 1)
	runtime.beforeStart = func() {
		application.Connection.mu.Lock()
		started := application.Connection.started
		application.Connection.mu.Unlock()
		startedBeforeLifecycle <- started
	}

	application.Run()
	if started := <-startedBeforeLifecycle; started {
		t.Fatal("desktop session watcher started before the Fyne driver lifecycle")
	}
	application.Connection.mu.Lock()
	startedAfterLifecycle := application.Connection.started
	application.Connection.mu.Unlock()
	if !startedAfterLifecycle {
		t.Fatal("desktop session watcher did not start from the driver lifecycle")
	}
}

func TestApplicationRestoresSourceBeforeWindowAndDoesNotReapplyIt(t *testing.T) {
	base := test.NewApp()
	runtime := &startupLifecycleApp{
		App:        base,
		lifecycle:  &startupLifecycle{},
		runEntered: make(chan struct{}),
	}
	store := &MemorySourceStore{}
	if err := store.Save(context.Background(), []byte("https://saved.example/profile")); err != nil {
		t.Fatal(err)
	}
	application := NewApplication(runtime, &fakeClient{}, store)
	t.Cleanup(application.Close)
	events := []string{}
	window := &startupTrackingWindow{Window: application.Window, events: &events}
	window.beforeShow = func() { application.Connection.Input.SetText("typed-before-window") }
	application.Window = window
	application.Run()
	if got := application.Connection.Input.Text; got != "typed-before-window" {
		t.Fatalf("source was reapplied after the window became interactive: %q", got)
	}
	if len(events) != 1 || events[0] != "show" {
		t.Fatalf("window events = %v, want [show]", events)
	}
}

func (r *reconnectClient) Configure(context.Context, []byte, uint64) (ConfigureResult, error) {
	return ConfigureResult{}, nil
}
func (r *reconnectClient) Start(context.Context, uint64) (StartResult, error) {
	return StartResult{}, nil
}
func (r *reconnectClient) Stop(context.Context, uint64) (StopResult, error) {
	return StopResult{}, nil
}
func (r *reconnectClient) Snapshot(context.Context) (Snapshot, error) {
	return Snapshot{State: StateConnected, Generation: 1}, nil
}
func (r *reconnectClient) Watch(context.Context) (<-chan Snapshot, error) {
	r.mu.Lock()
	r.watches++
	watch := r.watches
	r.mu.Unlock()
	if watch == 1 {
		closed := make(chan Snapshot)
		close(closed)
		return closed, nil
	}
	return r.updates, nil
}
func (r *reconnectClient) Reset(context.Context, uint64) (Snapshot, error) {
	return Snapshot{}, nil
}

func (f *fakeClient) Configure(_ context.Context, raw []byte, _ uint64) (ConfigureResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.configuredRaw = append(f.configuredRaw[:0], raw...)
	return f.configure, nil
}
func (f *fakeClient) Start(context.Context, uint64) (StartResult, error) {
	f.mu.Lock()
	gate := f.startGate
	f.mu.Unlock()
	if gate != nil {
		<-gate
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = true
	if f.startedCh != nil {
		close(f.startedCh)
		f.startedCh = nil
	}
	return StartResult{Generation: 4, Sequence: f.configure.Sequence + 1}, nil
}
func (f *fakeClient) Stop(context.Context, uint64) (StopResult, error) {
	f.mu.Lock()
	gate := f.stopGate
	f.mu.Unlock()
	if gate != nil {
		<-gate
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = true
	return StopResult{Generation: 4, Sequence: 9}, nil
}

func TestConnectionViewDisablesControlWhileDisconnecting(t *testing.T) {
	runtime := test.NewApp()
	defer runtime.Quit()
	gate := make(chan struct{})
	client := &fakeClient{stopGate: gate}
	view := NewConnectionView(client)
	view.ctx = context.Background()
	view.render(Snapshot{State: StateConnected, Generation: 4})

	view.toggle()
	if !view.Connect.Disabled() {
		t.Fatal("Disconnect control remained enabled while session stop was in flight")
	}

	close(gate)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && view.Connect.Disabled() {
		time.Sleep(5 * time.Millisecond)
	}
	if view.Connect.Disabled() {
		t.Fatal("Disconnect control remained disabled after session stop completed")
	}
	if !client.stoppedValue() {
		t.Fatal("session client was not stopped")
	}
}
func (f *fakeClient) Snapshot(context.Context) (Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.snapshot, nil
}
func (f *fakeClient) Watch(context.Context) (<-chan Snapshot, error) {
	updates := make(chan Snapshot)
	close(updates)
	return updates, nil
}
func (f *fakeClient) Reset(context.Context, uint64) (Snapshot, error) { return Snapshot{}, nil }

func (f *fakeClient) startedValue() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.started
}

func (f *fakeClient) setStarted(value bool) {
	f.mu.Lock()
	f.started = value
	f.mu.Unlock()
}

func (f *fakeClient) stoppedValue() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stopped
}

func (f *fakeClient) configuredSource() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]byte(nil), f.configuredRaw...)
}

func TestAccessibleEntryStagesMultilinePasteOutsideTheRenderer(t *testing.T) {
	runtime := test.NewApp()
	defer runtime.Quit()
	entry := newAccessibleEntry(true, "Connection configuration", true)
	raw := "[[Outline]]\nendpoint = \"example.invalid\"\npassword = \"synthetic\"\n"
	clipboard := test.NewClipboard()
	clipboard.SetContent(raw)

	entry.TypedShortcut(&fyne.ShortcutPaste{Clipboard: clipboard})

	if entry.Text != inlinePasteSummary {
		t.Fatalf("visible text = %q, want fixed summary", entry.Text)
	}
	if entry.SourceText() != raw {
		t.Fatalf("staged source was not preserved exactly")
	}
	if strings.Contains(entry.Text, "example.invalid") || strings.Contains(entry.Text, "synthetic") {
		t.Fatal("visible summary exposed inline source content")
	}
}

func TestAccessibleEntryKeepsOrdinaryPasteAndClearsStagedSourceOnMutation(t *testing.T) {
	runtime := test.NewApp()
	defer runtime.Quit()
	entry := newAccessibleEntry(true, "Connection configuration", true)
	clipboard := test.NewClipboard()
	clipboard.SetContent("https://example.test/profile")
	entry.TypedShortcut(&fyne.ShortcutPaste{Clipboard: clipboard})
	if entry.Text != "https://example.test/profile" || entry.SourceText() != entry.Text {
		t.Fatalf("one-line paste did not retain ordinary Entry behavior: %q", entry.Text)
	}

	clipboard.SetContent("[[Outline]]\nendpoint = \"example.invalid\"\n")
	entry.TypedShortcut(&fyne.ShortcutPaste{Clipboard: clipboard})
	if entry.SourceText() == entry.Text {
		t.Fatal("multiline paste was not staged")
	}
	entry.Entry.SetText("manually replaced")
	if got := entry.SourceText(); got != "manually replaced" {
		t.Fatalf("direct embedded SetText left stale staged source: %q", got)
	}
	logs := NewAccessibleEntry(true, "Connection logs")
	logs.TypedShortcut(&fyne.ShortcutPaste{Clipboard: clipboard})
	if logs.Text != clipboard.Content() || logs.SourceText() != clipboard.Content() {
		t.Fatal("the Logs entry unexpectedly staged multiline paste")
	}
}

func TestConnectionViewSubmitsExactStagedMultilineSource(t *testing.T) {
	runtime := test.NewApp()
	defer runtime.Quit()
	started := make(chan struct{})
	client := &fakeClient{
		configure: ConfigureResult{Sequence: 2},
		startedCh: started,
	}
	view := NewConnectionView(client)
	view.ctx = context.Background()
	raw := "[[Outline]]\nendpoint = \"example.invalid\"\npassword = \"synthetic\"\n"
	clipboard := test.NewClipboard()
	clipboard.SetContent(raw)
	view.Input.TypedShortcut(&fyne.ShortcutPaste{Clipboard: clipboard})
	view.toggle()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("staged source did not reach Connect")
	}
	if got := string(client.configuredSource()); got != raw {
		t.Fatalf("configured source = %q, want exact staged source", got)
	}
}

func TestConnectionViewRendersAuthoritativeSnapshot(t *testing.T) {
	runtime := test.NewApp()
	defer runtime.Quit()
	client := &fakeClient{snapshot: Snapshot{
		State:         StateConnected,
		Sequence:      7,
		Generation:    3,
		ActiveProfile: &Profile{Protocol: ProtocolOutline, Description: "primary"},
	}}
	view := NewConnectionView(client)
	view.render(client.snapshot)

	if view.Status.Text != "Connected" {
		t.Fatalf("status = %q", view.Status.Text)
	}
	if view.Connect.Text != "Disconnect" {
		t.Fatalf("button = %q", view.Connect.Text)
	}
	if view.Details.Text != "OUTLINE — primary" {
		t.Fatalf("details = %q", view.Details.Text)
	}
}

func TestConnectionViewPrimeRefreshesSessionRevision(t *testing.T) {
	runtime := test.NewApp()
	defer runtime.Quit()
	client := &fakeClient{snapshot: Snapshot{State: StateIdle, Sequence: 7}}
	view := NewConnectionView(client)
	if err := view.Prime(context.Background()); err != nil {
		t.Fatalf("prime failed: %v", err)
	}
	if view.sequence != 7 {
		t.Fatalf("sequence = %d, want 7", view.sequence)
	}
}

func TestConnectionViewShowsRecoveryAndFailureDetails(t *testing.T) {
	runtime := test.NewApp()
	defer runtime.Quit()
	view := NewConnectionView(nil)
	view.render(Snapshot{State: StateIdle, Recovering: true})
	if view.Status.Text != "Reconnecting" {
		t.Fatalf("recovery status = %q", view.Status.Text)
	}
	view.render(Snapshot{State: StateFailed, LastFailure: &Failure{Code: "RUNTIME_FAILED", Message: "bridge unavailable"}})
	if view.Details.Text != "RUNTIME_FAILED: bridge unavailable" {
		t.Fatalf("failure details = %q", view.Details.Text)
	}
}

func TestConnectionViewRejectsEmptyConfiguration(t *testing.T) {
	runtime := test.NewApp()
	defer runtime.Quit()
	client := &fakeClient{}
	view := NewConnectionView(client)
	view.ctx = context.Background()
	view.toggle()
	if view.Status.Text != "Error" {
		t.Fatalf("status = %q", view.Status.Text)
	}
	if view.Details.Text != "INVALID_ARGUMENT: connection configuration is required" {
		t.Fatalf("validation details = %q", view.Details.Text)
	}
}

func TestConnectionViewKeepsLocalErrorUntilSessionAdvances(t *testing.T) {
	runtime := test.NewApp()
	defer runtime.Quit()
	view := NewConnectionView(nil)
	view.render(Snapshot{State: StateIdle, Sequence: 7})
	view.showError(context.DeadlineExceeded)

	view.render(Snapshot{State: StateIdle, Sequence: 7})
	if view.Status.Text != "Error" || view.Details.Text != context.DeadlineExceeded.Error() {
		t.Fatalf("stale snapshot replaced local error: status=%q details=%q", view.Status.Text, view.Details.Text)
	}

	view.render(Snapshot{State: StateIdle, Sequence: 7, Recovering: true})
	if view.Status.Text != "Error" || view.Details.Text != context.DeadlineExceeded.Error() {
		t.Fatalf("transport recovery replaced local error: status=%q details=%q", view.Status.Text, view.Details.Text)
	}

	view.render(Snapshot{State: StateConfigured, Sequence: 8})
	if view.Status.Text != "Ready" {
		t.Fatalf("new session state did not replace local error: %q", view.Status.Text)
	}
}

func TestConnectionViewButtonDrivesSessionClient(t *testing.T) {
	runtime := test.NewApp()
	defer runtime.Quit()
	started := make(chan struct{})
	client := &fakeClient{
		configure: ConfigureResult{Sequence: 2},
		startedCh: started,
	}
	view := NewConnectionView(client)
	view.ctx = context.Background()
	test.Type(view.Input, "https://example.test/profile")
	test.Tap(view.Connect)

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("visible Connect control did not start the session")
	}
	if !client.startedValue() {
		t.Fatal("session client was not started")
	}
}

func TestConnectionViewPublishesConnectingBeforeStartingSession(t *testing.T) {
	runtime := test.NewApp()
	defer runtime.Quit()
	gate := make(chan struct{})
	client := &fakeClient{startGate: gate}
	view := NewConnectionView(client)
	view.ctx = context.Background()
	view.render(Snapshot{State: StateIdle})
	view.showError(context.DeadlineExceeded)
	view.Input.SetText("https://example.test/profile")

	view.toggle()
	status, details, button := view.Presentation()
	if status != "Connecting" || details != "" || button != "Disconnect" {
		t.Fatalf("presentation during connect = (%q, %q, %q), want Connecting/empty/Disconnect", status, details, button)
	}
	if !view.Connect.Disabled() {
		t.Fatal("Connect control remained enabled while session start was in flight")
	}

	close(gate)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && !client.startedValue() {
		time.Sleep(5 * time.Millisecond)
	}
	if !client.startedValue() {
		t.Fatal("session start did not complete")
	}
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) && view.Connect.Disabled() {
		time.Sleep(5 * time.Millisecond)
	}
	if view.Connect.Disabled() {
		t.Fatal("Connect control remained disabled after session start completed")
	}
}

func TestConnectionViewPersistsOnlyAcceptedSourceAndCanRestartConfiguredSession(t *testing.T) {
	runtime := test.NewApp()
	defer runtime.Quit()
	store := &MemorySourceStore{}
	client := &fakeClient{
		snapshot:  Snapshot{State: StateIdle, Configured: true, Sequence: 2},
		configure: ConfigureResult{Sequence: 3, SourceKind: "URL"},
	}
	view := NewConnectionView(client, store)
	view.ctx = context.Background()
	view.render(client.snapshot)
	test.Type(view.Input, "https://example.test/accepted")
	view.toggle()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && !client.startedValue() {
		time.Sleep(5 * time.Millisecond)
	}
	got, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "https://example.test/accepted" {
		t.Fatalf("stored source = %q", got)
	}

	client.setStarted(false)
	view.Input.SetText("")
	view.mu.Lock()
	view.snapshot.Configured = true
	view.mu.Unlock()
	view.toggle()
	for time.Now().Before(deadline) && !client.startedValue() {
		time.Sleep(5 * time.Millisecond)
	}
	if !client.startedValue() {
		t.Fatal("configured session was not started without re-entering source")
	}
}

func TestConnectionViewDoesNotPersistInlineConfiguration(t *testing.T) {
	runtime := test.NewApp()
	defer runtime.Quit()
	store := &MemorySourceStore{}
	client := &fakeClient{
		configure: ConfigureResult{Sequence: 3, SourceKind: "INLINE"},
	}
	view := NewConnectionView(client, store)
	view.ctx = context.Background()
	test.Type(view.Input, "inline-secret-config")
	view.toggle()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && !client.startedValue() {
		time.Sleep(5 * time.Millisecond)
	}
	got, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("inline configuration was persisted: %q", got)
	}
}

func TestConnectionViewRendersWarningsInLogs(t *testing.T) {
	runtime := test.NewApp()
	defer runtime.Quit()
	view := NewConnectionView(nil)
	view.render(Snapshot{State: StateConfigured, Warnings: []Warning{{Code: "PROFILE_WARNING", Message: "profile ignored"}}})
	if view.Logs.Text != "PROFILE_WARNING: profile ignored" {
		t.Fatalf("logs = %q", view.Logs.Text)
	}
}

func TestConnectionViewExportsOnlyAuthoritativeDiagnostics(t *testing.T) {
	runtime := test.NewApp()
	defer runtime.Quit()
	exporter := &fakeLogExporter{}
	view := NewConnectionViewWithLogExporter(nil, exporter)
	view.render(Snapshot{
		State:       StateFailed,
		Warnings:    []Warning{{Code: "PROFILE_WARNING", Message: "profile ignored"}},
		LastFailure: &Failure{Code: "RUNTIME_FAILED", Message: "bridge unavailable"},
	})
	if view.Export.OnTapped == nil {
		t.Fatal("export button has no callback")
	}
	view.Export.OnTapped()
	got := exporter.captured()
	want := []string{
		"PROFILE_WARNING: profile ignored",
		"RUNTIME_FAILED: bridge unavailable",
	}
	if len(got) != len(want) {
		t.Fatalf("exported %d lines, want %d: %q", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("exported line %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestConnectionViewKeepsExportAccessibilityNameStable(t *testing.T) {
	view := NewConnectionViewWithLogExporter(nil, &fakeLogExporter{})
	if view.Export.Disabled() {
		t.Fatal("injected exporter left export button disabled")
	}
	if view.Export.Text != "Export logs" {
		t.Fatalf("export text = %q", view.Export.Text)
	}
	if view.Export.AccessibilityLabel() != "Export logs" {
		t.Fatalf("export accessibility label = %q", view.Export.AccessibilityLabel())
	}
}

func TestAccessibleEntryMobileTouchFocusesTheWrappedCanvasObject(t *testing.T) {
	runtime := test.NewApp()
	defer runtime.Quit()
	view := NewConnectionView(nil)
	window := runtime.NewWindow("")
	window.SetContent(view.Content())
	window.Resize(fyne.NewSize(460, 520))
	window.Show()
	t.Cleanup(window.Close)

	// TouchDown is the mobile equivalent of the real tap path. Rendering once
	// initializes Entry's cursor/selection state before the synthetic event.
	test.WidgetRenderer(view.Input)
	view.Input.TouchDown(&mobile.TouchEvent{
		PointEvent: fyne.PointEvent{Position: fyne.NewPos(1, 1)},
	})
	if focused := window.Canvas().Focused(); focused != view.Input {
		t.Fatalf("focused object = %T, want the accessible entry wrapper", focused)
	}
}

func TestConnectionViewReconnectsAfterWatchClosure(t *testing.T) {
	runtime := test.NewApp()
	defer runtime.Quit()
	client := &reconnectClient{updates: make(chan Snapshot, 1)}
	view := NewConnectionView(client)
	view.Start()
	t.Cleanup(view.Stop)

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && view.displayedStatus() != "Reconnecting" {
		time.Sleep(10 * time.Millisecond)
	}
	if view.displayedStatus() != "Reconnecting" {
		t.Fatalf("status after watch closure = %q", view.displayedStatus())
	}
	client.updates <- Snapshot{State: StateConnected, Generation: 2}
	for time.Now().Before(deadline) && view.displayedStatus() != "Connected" {
		time.Sleep(10 * time.Millisecond)
	}
	if view.displayedStatus() != "Connected" {
		t.Fatalf("status after watch reconnect = %q", view.displayedStatus())
	}
}

func TestSettingsViewContainsBuildIdentity(t *testing.T) {
	oldVersion, oldCommit := Version, Commit
	Version, Commit = "1.5.1", "abc123"
	t.Cleanup(func() { Version, Commit = oldVersion, oldCommit })

	view := NewSettingsView()
	if view.Version.Text != "Version: 1.5.1" {
		t.Fatalf("version = %q", view.Version.Text)
	}
	if view.Commit.Text != "Source commit: abc123" {
		t.Fatalf("commit = %q", view.Commit.Text)
	}
	if view.Commit.URL == nil || view.Commit.URL.String() != "https://github.com/DobbyVPN/DobbyVPN/tree/abc123" {
		t.Fatalf("commit URL = %v", view.Commit.URL)
	}
}

func TestSettingsViewDoesNotInventMissingBuildIdentity(t *testing.T) {
	oldVersion, oldCommit := Version, Commit
	Version, Commit = " ", "unknown"
	t.Cleanup(func() { Version, Commit = oldVersion, oldCommit })

	view := NewSettingsView()
	if view.Version.Text != "Version: development" {
		t.Fatalf("version = %q", view.Version.Text)
	}
	if view.Commit.Text != "Source commit: unknown" {
		t.Fatalf("commit = %q", view.Commit.Text)
	}
	if view.Commit.URL != nil {
		t.Fatalf("unknown commit unexpectedly linked to %v", view.Commit.URL)
	}
}

func TestApplicationCloseDetachesWithoutStoppingSession(t *testing.T) {
	runtime := test.NewApp()
	defer runtime.Quit()
	client := &fakeClient{}
	application := NewApplication(runtime, client)
	application.Start()
	application.Close()
	if client.stoppedValue() {
		t.Fatal("closing the UI must not stop the service-owned session")
	}
}

func TestApplicationControlsHaveStableLabelsAndSettingsRoundTrip(t *testing.T) {
	runtime := test.NewApp()
	defer runtime.Quit()
	client := &fakeClient{snapshot: Snapshot{State: StateConnected, Generation: 1}}
	application := NewApplication(runtime, client)
	application.Connection.render(client.snapshot)
	t.Cleanup(application.Close)

	if application.Connection.Input.AccessibilityLabel() != "Connection configuration" {
		t.Fatalf("configuration label = %q", application.Connection.Input.AccessibilityLabel())
	}
	if application.Connection.Connect.AccessibilityLabel() != "Disconnect" {
		t.Fatalf("connect label = %q", application.Connection.Connect.AccessibilityLabel())
	}
	if application.Connection.Status.AccessibilityLabel() != "Connected" {
		t.Fatalf("status label = %q", application.Connection.Status.AccessibilityLabel())
	}

	test.Tap(application.Connection.Settings)
	if application.Window.Content() != application.Settings.Content() {
		t.Fatal("settings tap did not replace the connection content")
	}
	test.Tap(application.Settings.Back)
	if application.Window.Content() != application.Connection.Content() {
		t.Fatal("back tap did not restore the connection content")
	}
}
