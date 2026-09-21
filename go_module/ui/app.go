package ui

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

const statusDisconnected = "Disconnected"

// Application owns only the Fyne window.  The VPN service and SessionClient
// outlive the window, so closing and reopening the UI never stops a tunnel.
type Application struct {
	App        fyne.App
	Window     fyne.Window
	Connection *ConnectionView
	Settings   *SettingsView
}

func NewApplication(runtime fyne.App, client SessionClient, stores ...SourceStore) *Application {
	return newApplication(runtime, client, nil, newDefaultDiagnosticStore(), stores...)
}

// NewApplicationWithLogExporter is the constructor used when a platform has
// an existing native save/share implementation. Keeping the exporter behind
// this one injected interface leaves session ownership and snapshot mapping
// in the shared UI.
func NewApplicationWithLogExporter(runtime fyne.App, client SessionClient, exporter LogExporter, stores ...SourceStore) *Application {
	return newApplication(runtime, client, exporter, newDefaultDiagnosticStore(), stores...)
}

// NewApplicationWithDiagnostics is used by mobile/native shells that own a
// platform-specific diagnostic location. Passing nil keeps the history and
// clear controls disabled until a native adapter is available.
func NewApplicationWithDiagnostics(runtime fyne.App, client SessionClient, diagnostics DiagnosticStore, stores ...SourceStore) *Application {
	return newApplication(runtime, client, nil, diagnostics, stores...)
}

// NewApplicationWithLogExporterAndDiagnostics combines the explicit share
// adapter with an injected retained-history adapter. The two boundaries stay
// separate because some platforms can read logs but cannot present a file
// picker from the shared Go process.
func NewApplicationWithLogExporterAndDiagnostics(runtime fyne.App, client SessionClient, exporter LogExporter, diagnostics DiagnosticStore, stores ...SourceStore) *Application {
	return newApplication(runtime, client, exporter, diagnostics, stores...)
}

func newApplication(runtime fyne.App, client SessionClient, exporter LogExporter, diagnostics DiagnosticStore, stores ...SourceStore) *Application {
	var store SourceStore
	if len(stores) > 0 {
		store = stores[0]
	}
	view := newConnectionView(client, exporter, diagnostics, store)
	settings := NewSettingsView()
	window := runtime.NewWindow("Dobby VPN")
	window.Resize(fyne.NewSize(460, 520))
	window.SetContent(view.Content())
	window.SetCloseIntercept(func() {
		view.Stop()
		window.Close()
	})
	view.Settings.OnTapped = func() { window.SetContent(settings.Content()) }
	settings.Back.OnTapped = func() { window.SetContent(view.Content()) }
	return &Application{App: runtime, Window: window, Connection: view, Settings: settings}
}

func (a *Application) Run() {
	a.run(nil)
}

// RunWithDiagnosticStore starts the native window before starting the mobile
// client or resolving a store whose implementation needs a platform context
// (for example Android's Fyne/JNI activity). Calling either path while the
// c-shared entrypoint is still constructing the window can race the first
// native surface callback; the store is optional, so its setup must not be on
// the first-render path.
func (a *Application) RunWithDiagnosticStore(resolve func() DiagnosticStore) {
	a.run(resolve)
}

func (a *Application) run(resolve func() DiagnosticStore) {
	// Restore the desktop source on the UI goroutine before the window is
	// shown.  The service watcher still starts from OnStarted, but it must not
	// be able to queue a persisted value after a native user has begun typing.
	// Mobile stores are injected after the native surface exists and are nil at
	// this point, so their lifecycle remains unchanged.
	a.Connection.loadSourceBeforeWindow(context.Background())
	// Keep Fyne's canonical ShowAndRun ordering for every native platform.  The
	// window must be created before Run; GLFW's OnStarted callback runs before
	// its event-loop select and is therefore not a post-surface boundary.  Only
	// service observation and platform diagnostic resolution belong behind that
	// lifecycle callback; the headless companion can still call Start directly.
	a.App.Lifecycle().SetOnStarted(func() {
		a.Start()
		if resolve != nil {
			go func() {
				store := resolve()
				fyne.Do(func() { a.Connection.SetDiagnosticStore(store) })
			}()
		}
	})
	a.Window.Show()
	markUIAttached()
	a.App.Run()
}

// Start attaches the UI to the service without opening a native window. It is
// used by the headless integration companion as well as by the normal app.
func (a *Application) Start() { a.Connection.Start() }

// Close detaches the UI observers and closes its window. The service owns the
// VPN session, so closing the UI never stops a healthy tunnel.
func (a *Application) Close() {
	a.Connection.Stop()
	a.Window.Close()
}

// ConnectionView is deliberately small and exposes its controls for native
// UI tests.  Tests locate controls by their stable accessibility labels/text,
// not by screen coordinates.
type ConnectionView struct {
	client      SessionClient
	store       SourceStore
	exporter    LogExporter
	diagnostics DiagnosticStore

	Input     *AccessibleEntry
	Connect   *widget.Button
	Status    *widget.Label
	Details   *widget.Label
	Logs      *AccessibleEntry
	Export    *widget.Button
	ClearLogs *widget.Button
	LogStatus *widget.Label
	Settings  *widget.Button
	root      fyne.CanvasObject

	mu           sync.Mutex
	ctx          context.Context
	cancel       context.CancelFunc
	done         chan struct{}
	snapshot     Snapshot
	started      bool
	busy         bool
	sequence     uint64
	generation   uint64
	sourceLoaded bool
	// rendered* mirrors the last authoritative presentation under mu. Native
	// widgets are updated through fyne.Do and must not be read from a worker
	// goroutine; the mirrors keep lifecycle tests race-free without making the
	// widget tree a second state store.
	renderedStatus    string
	renderedButton    string
	renderedDetails   string
	renderedLogs      string
	renderedLogStatus string
	exportLines       []string
	diagnosticsLoaded bool
	diagnosticError   string
	localError        string
	localErrorAt      uint64
	presentationMu    sync.Mutex
	diagnosticIO      sync.Mutex
}

func NewConnectionView(client SessionClient, stores ...SourceStore) *ConnectionView {
	return newConnectionView(client, nil, nil, stores...)
}

// NewConnectionViewWithLogExporter is useful to platform entry points and
// headless tests that want to exercise the export action without changing the
// SessionClient boundary.
func NewConnectionViewWithLogExporter(client SessionClient, exporter LogExporter, stores ...SourceStore) *ConnectionView {
	return newConnectionView(client, exporter, nil, stores...)
}

// NewConnectionViewWithDiagnostics injects a retained-history adapter without
// changing the SessionClient boundary.
func NewConnectionViewWithDiagnostics(client SessionClient, diagnostics DiagnosticStore, stores ...SourceStore) *ConnectionView {
	return newConnectionView(client, nil, diagnostics, stores...)
}

// NewConnectionViewWithLogExporterAndDiagnostics is the test and platform
// entrypoint for the complete history/export action pair.
func NewConnectionViewWithLogExporterAndDiagnostics(client SessionClient, exporter LogExporter, diagnostics DiagnosticStore, stores ...SourceStore) *ConnectionView {
	return newConnectionView(client, exporter, diagnostics, stores...)
}

func newConnectionView(client SessionClient, exporter LogExporter, diagnostics DiagnosticStore, stores ...SourceStore) *ConnectionView {
	var store SourceStore
	if len(stores) > 0 {
		store = stores[0]
	}
	view := &ConnectionView{
		client:         client,
		store:          store,
		exporter:       exporter,
		diagnostics:    diagnostics,
		renderedStatus: statusDisconnected,
		renderedButton: "Connect",
	}
	view.Input = NewAccessibleEntry(true, "Connection configuration")
	view.Input.SetPlaceHolder("HTTPS connection URL or inline configuration")
	view.Input.SetMinRowsVisible(4)

	view.Connect = widget.NewButton("Connect", nil)
	view.Status = widget.NewLabel(statusDisconnected)
	view.Details = widget.NewLabel("")
	view.Details.Wrapping = fyne.TextWrapWord
	view.Logs = NewAccessibleEntry(true, "Connection logs")
	view.Logs.SetMinRowsVisible(6)
	view.Logs.Disable()
	view.Export = widget.NewButton("Export logs", nil)
	if exporter == nil {
		view.Export.Disable()
	}
	view.ClearLogs = widget.NewButton("Clear logs", nil)
	if diagnostics == nil {
		// Mobile shells do not guess an app-group/private-filesystem path. Keep
		// the optional control out of the accessibility tree until a native
		// DiagnosticStore is injected, rather than exposing a dead button.
		view.ClearLogs.Hide()
	}
	view.LogStatus = widget.NewLabel("")
	view.LogStatus.Wrapping = fyne.TextWrapWord
	view.Settings = widget.NewButton("Settings", nil)

	view.Connect.OnTapped = func() { view.toggle() }
	view.Export.OnTapped = func() { view.exportLogs() }
	view.ClearLogs.OnTapped = func() { view.clearLogs() }
	view.root = container.NewBorder(
		container.NewVBox(
			view.Status,
			view.Details,
			view.Input,
			view.Connect,
			view.Export,
			view.ClearLogs,
			view.LogStatus,
			view.Settings,
		),
		nil, nil, nil, view.Logs,
	)
	return view
}

// SetLogExporter injects the platform adapter after a window exists. This is
// useful for desktop implementations whose file picker needs the Fyne window
// as its parent while retaining the old constructor for test companions.
func (v *ConnectionView) SetLogExporter(exporter LogExporter) {
	v.mu.Lock()
	v.exporter = exporter
	v.mu.Unlock()
	onUI(func() {
		if exporter == nil {
			v.Export.Disable()
		} else {
			v.Export.Enable()
		}
	})
}

// SetDiagnosticStore injects or replaces the retained-history adapter after a
// native window has been created. A failed or absent adapter never erases the
// last visible history; it only disables the clear action and reports status.
func (v *ConnectionView) SetDiagnosticStore(store DiagnosticStore) {
	v.mu.Lock()
	v.diagnostics = store
	v.diagnosticsLoaded = false
	v.mu.Unlock()
	onUI(func() {
		if store == nil {
			v.ClearLogs.Hide()
		} else {
			v.ClearLogs.Show()
			v.ClearLogs.Enable()
		}
	})
	if store != nil {
		v.refreshDiagnostics(context.Background())
	}
}

// AccessibleEntry supplies the label/role that Fyne's native accessibility
// bridges expose to UI Automation, UI Automator and XCTest. Fyne's standard
// Entry has no label of its own, so this wrapper keeps automation identifiers
// in application code instead of coordinates or screenshots.
type AccessibleEntry struct {
	*widget.Entry
	label string
}

func NewAccessibleEntry(multiline bool, label string) *AccessibleEntry {
	// Entry's focus and keyboard path resolves the canvas object through its
	// BaseWidget implementation.  Constructing an Entry with NewEntry first
	// binds that implementation to the inner *widget.Entry; when the entry is
	// then embedded in this accessibility wrapper, a mobile tap asks Fyne to
	// focus an object that is no longer present in the canvas tree.  Construct
	// the zero-value widget here and bind it to the wrapper before it is ever
	// rendered so both the renderer and the focus path use the same object.
	entry := &widget.Entry{Wrapping: fyne.TextWrap(fyne.TextTruncateClip)}
	if multiline {
		entry.MultiLine = true
	}
	view := &AccessibleEntry{Entry: entry, label: label}
	entry.ExtendBaseWidget(view)
	return view
}

func (e *AccessibleEntry) AccessibilityLabel() string { return e.label }
func (e *AccessibleEntry) AccessibilityRole() fyne.AccessibleRole {
	return fyne.AccessibleRoleText
}

func (v *ConnectionView) Content() fyne.CanvasObject { return v.root }

// Presentation returns the last authoritative presentation without reading
// Fyne widgets from a worker goroutine. The headless companion uses this
// synchronized view while native renderers update widgets through fyne.Do.
func (v *ConnectionView) Presentation() (status, details, button string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.renderedStatus, v.renderedDetails, v.renderedButton
}

// Configure validates and stores a profile without starting a generation.
// The desktop companion uses this operation to keep the semantic configure
// step separate from the visible Connect action.  That preserves the shared
// functional contract while still letting the following connect step exercise
// the production button callback.
func (v *ConnectionView) Configure(ctx context.Context, raw []byte) error {
	v.mu.Lock()
	sequence := v.sequence
	v.mu.Unlock()
	configured, err := v.client.Configure(ctx, raw, sequence)
	if err != nil {
		v.showError(err)
		return err
	}
	v.mu.Lock()
	v.sequence = configured.Sequence
	v.mu.Unlock()
	// Snapshot also refreshes the desktop client's opaque session ID after the
	// first request and keeps the widget presentation authoritative.
	snapshot, err := v.client.Snapshot(ctx)
	if err != nil {
		v.render(Snapshot{
			State:      StateConfigured,
			Configured: true,
			Digest:     configured.Digest,
			SourceKind: configured.SourceKind,
			Profiles:   configured.Profiles,
			Warnings:   configured.Warnings,
			Sequence:   configured.Sequence,
		})
		return err
	}
	v.render(snapshot)
	return nil
}

func (v *ConnectionView) Start() {
	v.mu.Lock()
	if v.started || v.client == nil {
		v.mu.Unlock()
		return
	}
	v.started = true
	v.ctx, v.cancel = context.WithCancel(context.Background())
	v.done = make(chan struct{})
	ctx := v.ctx
	done := v.done
	v.mu.Unlock()

	go func() {
		defer close(done)
		var group sync.WaitGroup
		group.Add(2)
		go func() {
			defer group.Done()
			v.watch(ctx)
		}()
		go func() {
			defer group.Done()
			v.watchDiagnostics(ctx)
		}()
		group.Wait()
	}()
}

func (v *ConnectionView) watchDiagnostics(ctx context.Context) {
	// The mobile entrypoint may inject its native store after Start, once the
	// Fyne window has a live platform context. Keep the watcher alive while
	// that optional store is still nil; refreshDiagnostics deliberately treats
	// nil as a no-op and the next tick observes the injected store.
	v.refreshDiagnostics(ctx)
	// Diagnostic producers do not necessarily emit a session snapshot when a
	// new line is written. Keep the rollback UI's 500 ms refresh cadence, but
	// serialize every store operation through diagnosticIO so a slow read never
	// overlaps a later poll or a user-triggered clear.
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			v.refreshDiagnostics(ctx)
		case <-ctx.Done():
			return
		}
	}
}

// Prime synchronously obtains the initial service snapshot.  The normal app
// has time for its watcher to populate this state before a user can click;
// the headless companion accepts commands immediately, so it uses Prime to
// establish the session ID and revision before its first mutation.
func (v *ConnectionView) Prime(ctx context.Context) error {
	snapshot, err := v.client.Snapshot(ctx)
	if err != nil {
		v.showError(err)
		return err
	}
	v.render(snapshot)
	return nil
}

func (v *ConnectionView) Stop() {
	v.mu.Lock()
	done := v.done
	if v.cancel != nil {
		v.cancel()
	}
	v.done = nil
	v.started = false
	v.mu.Unlock()
	if done != nil {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			// A native transport is allowed to finish after the window has
			// closed. Never block the UI shutdown indefinitely on it.
		}
	}
}

func (v *ConnectionView) watch(ctx context.Context) {
	var last Snapshot
	haveSnapshot := false
	backoff := 100 * time.Millisecond
	for {
		if !haveSnapshot {
			snapshot, err := v.client.Snapshot(ctx)
			switch {
			case err == nil:
				v.loadSource(ctx)
				last = snapshot
				haveSnapshot = true
				v.render(snapshot)
				backoff = 100 * time.Millisecond
			case !v.waitForReconnect(ctx, last, backoff):
				return
			default:
				backoff = nextReconnectDelay(backoff)
				continue
			}
		}

		updates, err := v.client.Watch(ctx)
		if err != nil {
			v.renderRecovering(last)
			if !v.waitForReconnect(ctx, last, backoff) {
				return
			}
			backoff = nextReconnectDelay(backoff)
			haveSnapshot = false
			continue
		}
		for {
			select {
			case snapshot, ok := <-updates:
				if !ok {
					v.renderRecovering(last)
					if !v.waitForReconnect(ctx, last, backoff) {
						return
					}
					backoff = nextReconnectDelay(backoff)
					haveSnapshot = false
					goto reconnect
				}
				last = snapshot
				v.render(snapshot)
				backoff = 100 * time.Millisecond
			case <-ctx.Done():
				return
			}
		}
	reconnect:
		continue
	}
}

func (v *ConnectionView) waitForReconnect(ctx context.Context, last Snapshot, delay time.Duration) bool {
	v.renderRecovering(last)
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func nextReconnectDelay(current time.Duration) time.Duration {
	if current >= 2*time.Second {
		return 2 * time.Second
	}
	return current * 2
}

func (v *ConnectionView) renderRecovering(last Snapshot) {
	last.Recovering = true
	v.render(last)
}

func (v *ConnectionView) toggle() {
	v.mu.Lock()
	if v.busy {
		v.mu.Unlock()
		return
	}
	snapshot := v.snapshot
	if snapshot.State == StateConnected || snapshot.State == StateProbing || snapshot.State == StatePreparing || snapshot.State == StateStopping {
		v.busy = true
		generation := v.generation
		ctx := v.ctx
		v.mu.Unlock()
		v.applyPresentation()
		go v.disconnect(ctx, generation)
		return
	}
	v.localError = ""
	v.busy = true
	sequence := v.sequence
	text := strings.TrimSpace(v.Input.Text)
	ctx := v.ctx
	// Publish the attempt synchronously before the transport work starts.  A
	// previous permission or transport error is otherwise still rendered while
	// the retry is being configured, so a real UI observer can mistake that
	// stale frame for the result of the new attempt.
	v.renderedStatus = "Connecting"
	v.renderedButton = "Disconnect"
	v.renderedDetails = ""
	v.mu.Unlock()
	v.applyPresentation()

	if text == "" && !snapshot.Configured {
		// Keep the local validation failure in the same fixed vocabulary as
		// session Configure errors.  The rendered Android lane can then
		// distinguish an empty/corrupted editor value from a native transport
		// failure without reading or exporting the entered configuration.
		v.showError(fmt.Errorf("INVALID_ARGUMENT: connection configuration is required"))
		v.clearBusy()
		return
	}
	if text == "" {
		go v.startConfigured(ctx, sequence)
		return
	}
	go v.connect(ctx, []byte(text), sequence)
}

func (v *ConnectionView) exportLogs() {
	v.mu.Lock()
	exporter := v.exporter
	snapshot := v.snapshot
	lines := append([]string(nil), v.exportLines...)
	loaded := v.diagnosticsLoaded
	ctx := v.ctx
	v.mu.Unlock()
	if exporter == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if !loaded {
		lines = snapshotLogLines(snapshot)
	}
	if err := exporter.Export(ctx, lines); err != nil {
		v.setDiagnosticError(err)
	}
}

func (v *ConnectionView) clearLogs() {
	v.mu.Lock()
	store := v.diagnostics
	ctx := v.ctx
	v.mu.Unlock()
	if store == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	v.diagnosticIO.Lock()
	err := store.Clear(ctx)
	v.diagnosticIO.Unlock()
	if err != nil {
		v.setDiagnosticError(err)
		return
	}
	// Refresh from storage after the clear marker is durably written. This is
	// deliberately not an optimistic blanking operation: if the read fails,
	// the previous retained history remains visible.
	v.refreshDiagnostics(ctx)
}

func (v *ConnectionView) refreshDiagnostics(ctx context.Context) {
	v.mu.Lock()
	store := v.diagnostics
	v.mu.Unlock()
	if store == nil {
		return
	}
	v.diagnosticIO.Lock()
	history, err := store.Read(ctx)
	v.diagnosticIO.Unlock()
	v.mu.Lock()
	if len(history.UILines) > 0 || err == nil {
		v.diagnosticsLoaded = true
		v.renderedLogs = strings.Join(history.UILines, "\n")
		v.exportLines = append([]string(nil), history.ExportLines...)
	}
	if err != nil {
		v.diagnosticError = err.Error()
	} else {
		v.diagnosticError = ""
	}
	v.renderedLogStatus = diagnosticStatusText(v.diagnosticError)
	v.mu.Unlock()
	v.applyPresentation()
}

func (v *ConnectionView) setDiagnosticError(err error) {
	if err == nil {
		return
	}
	v.mu.Lock()
	v.diagnosticError = err.Error()
	v.renderedLogStatus = diagnosticStatusText(v.diagnosticError)
	v.mu.Unlock()
	v.applyPresentation()
}

func (v *ConnectionView) connect(ctx context.Context, raw []byte, sequence uint64) {
	configured, err := v.client.Configure(ctx, raw, sequence)
	if err == nil {
		v.mu.Lock()
		v.sequence = configured.Sequence
		v.mu.Unlock()
		// Only URL sources are safe to restore on the next launch. Inline
		// configuration may contain credentials and is intentionally kept in the
		// service-owned session only.
		if v.store != nil && strings.EqualFold(strings.TrimSpace(configured.SourceKind), "URL") {
			if saveErr := v.store.Save(ctx, raw); saveErr != nil {
				err = fmt.Errorf("save accepted connection source: %w", saveErr)
			}
		}
	}
	if err == nil {
		_, err = v.client.Start(ctx, configured.Sequence)
	}
	if err != nil {
		v.showError(err)
	}
	v.clearBusy()
}

func (v *ConnectionView) startConfigured(ctx context.Context, sequence uint64) {
	_, err := v.client.Start(ctx, sequence)
	if err != nil {
		v.showError(err)
	}
	v.clearBusy()
}

func (v *ConnectionView) loadSource(ctx context.Context) {
	store := v.claimSourceStore()
	if store == nil {
		return
	}
	raw, err := store.Load(ctx)
	if err != nil || len(raw) == 0 {
		if err != nil {
			v.showError(err)
		}
		return
	}
	text := string(raw)
	onUI(func() { v.Input.SetText(text) })
}

// loadSourceBeforeWindow is used by desktop startup, where the UI goroutine
// is the caller and the native window has not accepted input yet.  Applying
// the value directly closes the startup overwrite race that exists when the
// watcher queues SetText while AX has already exposed the Entry.
func (v *ConnectionView) loadSourceBeforeWindow(ctx context.Context) {
	store := v.claimSourceStore()
	if store == nil {
		return
	}
	raw, err := store.Load(ctx)
	if err != nil {
		v.showError(err)
		return
	}
	if len(raw) > 0 {
		v.Input.SetText(string(raw))
	}
}

func (v *ConnectionView) claimSourceStore() SourceStore {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.sourceLoaded || v.store == nil {
		return nil
	}
	v.sourceLoaded = true
	return v.store
}

func (v *ConnectionView) disconnect(ctx context.Context, generation uint64) {
	if generation == 0 {
		v.showError(fmt.Errorf("no active VPN generation"))
		v.clearBusy()
		return
	}
	if _, err := v.client.Stop(ctx, generation); err != nil {
		v.showError(err)
	}
	v.clearBusy()
}

func (v *ConnectionView) clearBusy() {
	v.mu.Lock()
	v.busy = false
	v.mu.Unlock()
	onUI(func() { v.Connect.Enable() })
}

func (v *ConnectionView) render(snapshot Snapshot) {
	status := statusText(snapshot)
	button := buttonText(snapshot)
	details := detailsText(snapshot)
	v.mu.Lock()
	keepLocalError := v.localError != "" &&
		snapshot.Sequence <= v.localErrorAt &&
		snapshot.State != StatePreparing &&
		snapshot.State != StateProbing &&
		snapshot.State != StateConnected &&
		snapshot.State != StateStopping &&
		snapshot.State != StateFailed
	if keepLocalError {
		status = "Error"
		details = v.localError
	} else {
		v.localError = ""
	}
	v.snapshot = snapshot
	v.sequence = snapshot.Sequence
	v.generation = snapshot.Generation
	v.renderedStatus = status
	v.renderedButton = button
	v.renderedDetails = details
	if !v.diagnosticsLoaded {
		v.renderedLogs = snapshotLogText(snapshot)
		v.exportLines = snapshotLogLines(snapshot)
	}
	v.renderedLogStatus = diagnosticStatusText(v.diagnosticError)
	v.mu.Unlock()

	v.applyPresentation()
}

func (v *ConnectionView) showError(err error) {
	details := err.Error()
	v.mu.Lock()
	v.localError = details
	v.localErrorAt = v.sequence
	v.renderedStatus = "Error"
	v.renderedDetails = details
	v.mu.Unlock()
	v.applyPresentation()
}

// applyPresentation reads the synchronized presentation only after its UI
// callback starts. If an older render callback was already queued, it will
// therefore apply the newest state instead of overwriting a later action
// error with a stale snapshot.
func (v *ConnectionView) applyPresentation() {
	onUI(func() {
		v.presentationMu.Lock()
		defer v.presentationMu.Unlock()
		v.mu.Lock()
		status := v.renderedStatus
		button := v.renderedButton
		details := v.renderedDetails
		logs := v.renderedLogs
		logStatus := v.renderedLogStatus
		busy := v.busy
		v.mu.Unlock()
		v.Status.SetText(status)
		v.Connect.SetText(button)
		if busy {
			v.Connect.Disable()
		} else {
			v.Connect.Enable()
		}
		v.Details.SetText(details)
		v.Logs.SetText(logs)
		v.LogStatus.SetText(logStatus)
	})
}

func (v *ConnectionView) displayedStatus() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.renderedStatus
}

func statusText(snapshot Snapshot) string {
	if snapshot.Recovering {
		return "Reconnecting"
	}
	switch snapshot.State {
	case StateConnected:
		return "Connected"
	case StateProbing, StatePreparing:
		return "Connecting"
	case StateStopping:
		return "Disconnecting"
	case StateConfigured:
		return "Ready"
	case StateFailed:
		return "Failed"
	case StateIdle:
		return statusDisconnected
	default:
		return statusDisconnected
	}
}

func buttonText(snapshot Snapshot) string {
	if snapshot.State == StateConnected || snapshot.State == StateProbing || snapshot.State == StatePreparing || snapshot.State == StateStopping {
		return "Disconnect"
	}
	return "Connect"
}

func detailsText(snapshot Snapshot) string {
	if snapshot.LastFailure != nil {
		return snapshot.LastFailure.Code + ": " + snapshot.LastFailure.Message
	}
	if snapshot.ActiveProfile != nil {
		return string(snapshot.ActiveProfile.Protocol) + " — " + snapshot.ActiveProfile.Description
	}
	if len(snapshot.Profiles) > 0 {
		return fmt.Sprintf("%d profile(s) available", len(snapshot.Profiles))
	}
	return ""
}

func snapshotLogText(snapshot Snapshot) string {
	return strings.Join(snapshotLogLines(snapshot), "\n")
}

func snapshotLogLines(snapshot Snapshot) []string {
	lines := make([]string, 0, len(snapshot.Warnings)+1)
	for _, warning := range snapshot.Warnings {
		line := warning.Code
		if warning.Message != "" {
			line += ": " + warning.Message
		}
		lines = append(lines, line)
	}
	if snapshot.LastFailure != nil {
		line := snapshot.LastFailure.Code
		if snapshot.LastFailure.Message != "" {
			line += ": " + snapshot.LastFailure.Message
		}
		lines = append(lines, line)
	}
	return lines
}

func diagnosticStatusText(err string) string {
	if strings.TrimSpace(err) == "" {
		return ""
	}
	return "Local diagnostics unavailable (LOCAL_LOG_STORAGE_UNAVAILABLE): " + err
}

func onUI(fn func()) {
	if fyne.CurrentApp() == nil {
		fn()
		return
	}
	fyne.Do(fn)
}
