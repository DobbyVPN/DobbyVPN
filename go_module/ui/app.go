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

// Application owns only the Fyne window.  The VPN service and SessionClient
// outlive the window, so closing and reopening the UI never stops a tunnel.
type Application struct {
	App        fyne.App
	Window     fyne.Window
	Connection *ConnectionView
	Settings   *SettingsView
}

func NewApplication(runtime fyne.App, client SessionClient) *Application {
	view := NewConnectionView(client)
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
	a.Start()
	a.Window.ShowAndRun()
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
	client SessionClient

	Input    *AccessibleEntry
	Connect  *widget.Button
	Status   *widget.Label
	Details  *widget.Label
	Logs     *AccessibleEntry
	Settings *widget.Button
	root     fyne.CanvasObject

	mu         sync.Mutex
	ctx        context.Context
	cancel     context.CancelFunc
	done       chan struct{}
	snapshot   Snapshot
	started    bool
	busy       bool
	sequence   uint64
	generation uint64
}

func NewConnectionView(client SessionClient) *ConnectionView {
	view := &ConnectionView{client: client}
	view.Input = NewAccessibleEntry(true, "Connection configuration")
	view.Input.SetPlaceHolder("HTTPS connection URL or inline configuration")
	view.Input.SetMinRowsVisible(4)

	view.Connect = widget.NewButton("Connect", nil)
	view.Status = widget.NewLabel("Disconnected")
	view.Details = widget.NewLabel("")
	view.Details.Wrapping = fyne.TextWrapWord
	view.Logs = NewAccessibleEntry(true, "Connection logs")
	view.Logs.SetMinRowsVisible(6)
	view.Logs.Disable()
	view.Settings = widget.NewButton("Settings", nil)

	view.Connect.OnTapped = func() { view.toggle() }
	view.root = container.NewBorder(
		container.NewVBox(
			view.Status,
			view.Details,
			view.Input,
			view.Connect,
			view.Settings,
		),
		nil, nil, nil, view.Logs,
	)
	return view
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
	entry := widget.NewEntry()
	if multiline {
		entry = widget.NewMultiLineEntry()
	}
	return &AccessibleEntry{Entry: entry, label: label}
}

func (e *AccessibleEntry) AccessibilityLabel() string { return e.label }
func (e *AccessibleEntry) AccessibilityRole() fyne.AccessibleRole {
	return fyne.AccessibleRoleText
}

func (v *ConnectionView) Content() fyne.CanvasObject { return v.root }

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
		v.watch(ctx)
	}()
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
		<-done
	}
}

func (v *ConnectionView) watch(ctx context.Context) {
	var last Snapshot
	haveSnapshot := false
	backoff := 100 * time.Millisecond
	for {
		if !haveSnapshot {
			snapshot, err := v.client.Snapshot(ctx)
			if err == nil {
				last = snapshot
				haveSnapshot = true
				v.render(snapshot)
				backoff = 100 * time.Millisecond
			} else if !v.waitForReconnect(ctx, last, backoff) {
				return
			} else {
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
		go v.disconnect(ctx, generation)
		return
	}
	v.busy = true
	sequence := v.sequence
	text := strings.TrimSpace(v.Input.Text)
	ctx := v.ctx
	v.mu.Unlock()

	if text == "" {
		v.showError(fmt.Errorf("connection configuration is required"))
		v.setBusy(false)
		return
	}
	go v.connect(ctx, []byte(text), sequence)
}

func (v *ConnectionView) connect(ctx context.Context, raw []byte, sequence uint64) {
	configured, err := v.client.Configure(ctx, raw, sequence)
	if err == nil {
		v.mu.Lock()
		v.sequence = configured.Sequence
		v.mu.Unlock()
		_, err = v.client.Start(ctx, configured.Sequence)
	}
	if err != nil {
		v.showError(err)
	}
	v.setBusy(false)
}

func (v *ConnectionView) disconnect(ctx context.Context, generation uint64) {
	if generation == 0 {
		v.showError(fmt.Errorf("no active VPN generation"))
		v.setBusy(false)
		return
	}
	if _, err := v.client.Stop(ctx, generation); err != nil {
		v.showError(err)
	}
	v.setBusy(false)
}

func (v *ConnectionView) setBusy(busy bool) {
	v.mu.Lock()
	v.busy = busy
	v.mu.Unlock()
	onUI(func() {
		if busy {
			v.Connect.Disable()
		} else {
			v.Connect.Enable()
		}
	})
}

func (v *ConnectionView) render(snapshot Snapshot) {
	v.mu.Lock()
	v.snapshot = snapshot
	v.sequence = snapshot.Sequence
	v.generation = snapshot.Generation
	v.mu.Unlock()

	onUI(func() {
		v.Status.SetText(statusText(snapshot))
		v.Connect.SetText(buttonText(snapshot))
		v.Details.SetText(detailsText(snapshot))
	})
}

func (v *ConnectionView) showError(err error) {
	onUI(func() {
		v.Status.SetText("Error")
		v.Details.SetText(err.Error())
	})
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
	default:
		return "Disconnected"
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

func onUI(fn func()) {
	if fyne.CurrentApp() == nil {
		fn()
		return
	}
	fyne.Do(fn)
}
