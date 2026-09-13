// Package mobilebinding exposes the session API through gomobile-safe values.
//
// It serializes only public state DTOs. Configuration bytes are accepted at
// the edge but are never returned in a result or callback.
package mobilebinding

import (
	"context"
	"encoding/json"
	"errors"

	v2 "go_module/sessionapi/v2"
)

// PlatformCallbacks is implemented by the Android service or iOS extension
// shell. State callbacks are wake hints; clients read Snapshot for the current
// authoritative state. Every callback carries the owner and generation so a
// delayed platform result cannot affect another connection attempt.
// AcquireTunnel must return a fresh duplicated descriptor owned by Go. Go
// closes that descriptor before ReleaseTunnel; a failed release prevents Go
// from reporting cleanup as complete.
type PlatformCallbacks interface {
	AcquireTunnel(sessionID string, generation int64) int32
	ReleaseTunnel(sessionID string, generation int64, fd int32) bool
	ProtectSocket(sessionID string, generation int64, fd int32) bool
	PublishState(sessionID string, generation int64, state string, failureCode string)
}

type managerAPI interface {
	Configure(context.Context, string, uint64, []byte) (v2.ConfigureResult, error)
	Start(context.Context, string, uint64, v2.StartTarget) (v2.StartResult, error)
	Stop(context.Context, string, uint64) (v2.StopResult, error)
	Snapshot(context.Context, string) (v2.SnapshotResult, error)
	Reset(context.Context, string, uint64) (v2.SnapshotResult, error)
}

// Binding is a thin synchronous JSON boundary over one process manager.
type Binding struct {
	manager  managerAPI
	platform platformControl //nolint:unused // Used by the android/ios platform adapter build.
}

// platformControl keeps native adapter mechanics out of the public binding
// surface while allowing mobile-tag files to install their one process bridge.
//
//nolint:unused // Implementations and calls are compiled only for android/ios.
type platformControl interface {
	setCallbacks(PlatformCallbacks)
	protectActive(int32) bool
}

// NewForTest permits pure tests to inject a manager without constructing native
// protocol implementations. Production mobile builds use New.
func NewForTest(manager managerAPI) *Binding { return &Binding{manager: manager} }

type envelope struct {
	OK     bool           `json:"ok"`
	Result interface{}    `json:"result,omitempty"`
	Error  *envelopeError `json:"error,omitempty"`
}

type envelopeError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func success(value interface{}) string { return encode(envelope{OK: true, Result: value}) }
func failed(err error) string {
	message := "internal session error"
	var domain *v2.Error
	if errors.As(err, &domain) {
		message = domain.Message
	}
	return encode(envelope{OK: false, Error: &envelopeError{Code: string(v2.CodeOf(err)), Message: message}})
}
func encode(value interface{}) string {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(data)
}

// Configure replaces accepted configuration only if the inspected snapshot is
// still current. The result identifies the new snapshot revision.
func (b *Binding) Configure(sessionID string, expectedSequence int64, rawConfig []byte) string {
	sequence, err := nonNegative(expectedSequence, "configuration sequence")
	if err != nil {
		return failed(err)
	}
	result, err := b.manager.Configure(context.Background(), sessionID, sequence, append([]byte(nil), rawConfig...))
	if err != nil {
		return failed(err)
	}
	return success(configureDTO(result))
}

// Start starts an attempt only if the inspected snapshot is still current.
func (b *Binding) Start(sessionID string, expectedSequence int64, mode string, index int32) string {
	sequence, err := nonNegative(expectedSequence, "start sequence")
	if err != nil {
		return failed(err)
	}
	if index < 0 && mode != string(v2.AutoSelect) {
		return failed(&v2.Error{Code: v2.FailureInvalidArgument, Message: "profile index must be non-negative"})
	}
	result, err := b.manager.Start(context.Background(), sessionID, sequence, v2.StartTarget{Mode: v2.StartMode(mode), Index: int(index)})
	if err != nil {
		return failed(err)
	}
	return success(startDTO(result))
}

// Stop stops only the requested generation. Repeating a completed stop is
// harmless according to the manager contract.
func (b *Binding) Stop(sessionID string, generation int64) string {
	if generation <= 0 {
		return failed(&v2.Error{Code: v2.FailureStaleGeneration, Message: "generation must be positive"})
	}
	result, err := b.manager.Stop(context.Background(), sessionID, uint64(generation))
	if err != nil {
		return failed(err)
	}
	return success(stopDTO(result))
}

// Snapshot attaches to the process-owned session when sessionID is empty.
func (b *Binding) Snapshot(sessionID string) string {
	result, err := b.manager.Snapshot(context.Background(), sessionID)
	if err != nil {
		return failed(err)
	}
	return success(snapshotDTO(result))
}

// Reset clears accepted configuration after successful cleanup and a matching
// snapshot revision.
func (b *Binding) Reset(sessionID string, expectedSequence int64) string {
	sequence, err := nonNegative(expectedSequence, "reset sequence")
	if err != nil {
		return failed(err)
	}
	result, err := b.manager.Reset(context.Background(), sessionID, sequence)
	if err != nil {
		return failed(err)
	}
	return success(snapshotDTO(result))
}

func nonNegative(value int64, name string) (uint64, error) {
	if value < 0 {
		return 0, &v2.Error{Code: v2.FailureInvalidArgument, Message: name + " must be non-negative"}
	}
	return uint64(value), nil
}

type profileDTO struct {
	Index       int32  `json:"index"`
	Protocol    string `json:"protocol"`
	Description string `json:"description"`
}
type warningDTO struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
type failureDTO struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
type configureResultDTO struct {
	Digest     string       `json:"digest"`
	Sequence   uint64       `json:"sequence,omitempty"`
	SourceKind string       `json:"source_kind"`
	Profiles   []profileDTO `json:"profiles"`
	Warnings   []warningDTO `json:"warnings"`
}
type startResultDTO struct {
	Generation uint64 `json:"generation"`
	Sequence   uint64 `json:"sequence"`
}
type stopResultDTO struct {
	Generation uint64 `json:"generation"`
	Sequence   uint64 `json:"sequence"`
}
type snapshotResultDTO struct {
	SessionID       string       `json:"session_id"`
	Sequence        uint64       `json:"sequence"`
	Generation      uint64       `json:"generation"`
	State           string       `json:"state"`
	Configured      bool         `json:"configured"`
	Digest          string       `json:"digest"`
	SourceKind      string       `json:"source_kind"`
	Profiles        []profileDTO `json:"profiles"`
	Warnings        []warningDTO `json:"warnings"`
	ActiveProfile   *profileDTO  `json:"active_profile,omitempty"`
	LastFailure     *failureDTO  `json:"last_failure,omitempty"`
	CleanupComplete bool         `json:"cleanup_complete"`
}

func profileResultDTO(in v2.ProfileSummary) profileDTO {
	return profileDTO{Index: in.Index, Protocol: string(in.Protocol), Description: in.Description}
}
func profileResultPtr(in *v2.ProfileSummary) *profileDTO {
	if in == nil {
		return nil
	}
	out := profileResultDTO(*in)
	return &out
}
func profilesDTO(in []v2.ProfileSummary) []profileDTO {
	out := make([]profileDTO, len(in))
	for i := range in {
		out[i] = profileResultDTO(in[i])
	}
	return out
}
func warningsDTO(in []v2.Warning) []warningDTO {
	out := make([]warningDTO, len(in))
	for i := range in {
		out[i] = warningDTO{Code: in[i].Code, Message: in[i].Message}
	}
	return out
}
func configureDTO(in v2.ConfigureResult) configureResultDTO {
	return configureResultDTO{
		Digest: in.Digest, Sequence: in.Sequence, SourceKind: string(in.SourceKind),
		Profiles: profilesDTO(in.Profiles), Warnings: warningsDTO(in.Warnings),
	}
}
func startDTO(in v2.StartResult) startResultDTO {
	return startResultDTO{Generation: in.Generation, Sequence: in.Sequence}
}
func stopDTO(in v2.StopResult) stopResultDTO {
	return stopResultDTO{Generation: in.Generation, Sequence: in.Sequence}
}
func snapshotDTO(in v2.SnapshotResult) snapshotResultDTO {
	out := snapshotResultDTO{
		SessionID: in.SessionID, Sequence: in.Sequence, Generation: in.Generation,
		State: string(in.State), Configured: in.Configured, Digest: in.Digest,
		SourceKind: string(in.SourceKind), Profiles: profilesDTO(in.Profiles),
		Warnings: warningsDTO(in.Warnings), ActiveProfile: profileResultPtr(in.ActiveProfile),
		CleanupComplete: in.CleanupComplete,
	}
	if in.LastFailure != "" {
		out.LastFailure = &failureDTO{Code: string(in.LastFailure), Message: in.LastFailureMessage}
	}
	return out
}
