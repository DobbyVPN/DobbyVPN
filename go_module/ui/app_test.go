package ui

import (
	"context"
	"testing"

	"fyne.io/fyne/v2/test"
)

type fakeClient struct {
	snapshot  Snapshot
	configure ConfigureResult
	started   bool
	stopped   bool
}

func (f *fakeClient) Configure(context.Context, []byte, uint64) (ConfigureResult, error) {
	return f.configure, nil
}
func (f *fakeClient) Start(context.Context, uint64) (StartResult, error) {
	f.started = true
	return StartResult{Generation: 4, Sequence: f.configure.Sequence + 1}, nil
}
func (f *fakeClient) Stop(context.Context, uint64) (StopResult, error) {
	f.stopped = true
	return StopResult{Generation: 4, Sequence: 9}, nil
}
func (f *fakeClient) Snapshot(context.Context) (Snapshot, error) { return f.snapshot, nil }
func (f *fakeClient) Watch(context.Context) (<-chan Snapshot, error) {
	updates := make(chan Snapshot)
	close(updates)
	return updates, nil
}
func (f *fakeClient) Reset(context.Context, uint64) (Snapshot, error) { return Snapshot{}, nil }

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
	if view.Details.Text == "" {
		t.Fatal("validation details are empty")
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
}
