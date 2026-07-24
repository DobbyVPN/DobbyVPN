// Package mobilebinding exposes the session API through gomobile-safe values.
//
// It deliberately serializes only the API's public DTOs.  Configuration bytes
// are accepted at the edge but are never included in a result or callback.
package mobilebinding

import (
	"context"
	"encoding/json"
	"math"

	"go_module/sessionapi/v1"
)

// PlatformCallbacks is implemented by the Android service or iOS extension
// shell.  Every callback carries both the session and its generation, so a
// delayed platform result cannot be applied to a later connection attempt.
// AcquireTunnel must return a newly duplicated descriptor owned by Go.  A
// negative result is an acquisition failure.  ReleaseTunnel is notification
// only: Go closes the owned descriptor before invoking it.
type PlatformCallbacks interface {
	AcquireTunnel(sessionID string, generation int64) int32
	ReleaseTunnel(sessionID string, generation int64, fd int32)
	ProtectSocket(sessionID string, generation int64, fd int32) bool
	PublishState(sessionID string, generation int64, sequence int64, state string, profileIndex int32, profileProtocol string, failureCode string)
}

type managerAPI interface {
	GetCapabilities(context.Context) v1.Capabilities
	CreateSession(context.Context) (string, error)
	Configure(context.Context, string, string, []byte) (v1.ConfigureResult, error)
	Start(context.Context, string, string, v1.StartTarget) (v1.StartResult, error)
	Stop(context.Context, string, string, uint64) (v1.StopResult, error)
	Snapshot(context.Context, string) (v1.SnapshotResult, error)
	Observe(context.Context, string, uint64) (v1.ObserveResult, error)
	DestroySession(context.Context, string) error
}

// Binding is a thin, synchronous command envelope over one process manager.
// It contains no configuration cache or VPN lifecycle state of its own.
type Binding struct {
	manager  managerAPI
	platform platformControl
}

// platformControl keeps native adapter mechanics out of the public binding
// surface while allowing mobile-tag files to install their one process bridge.
type platformControl interface {
	setCallbacks(PlatformCallbacks)
	queue(string, int32) error
	discard(string)
	protectActive(int32) bool
}

// NewForTest permits pure tests to inject a v1 manager without constructing
// native protocol implementations. Production mobile builds use New.
func NewForTest(manager managerAPI) *Binding { return &Binding{manager: manager} }

type envelope struct {
	OK     bool        `json:"ok"`
	Result interface{} `json:"result,omitempty"`
	Error  *safeError  `json:"error,omitempty"`
}

type safeError struct {
	Code string `json:"code"`
}

func success(value interface{}) string { return encode(envelope{OK: true, Result: value}) }
func failed(err error) string {
	return encode(envelope{OK: false, Error: &safeError{Code: string(v1.CodeOf(err))}})
}
func encode(value interface{}) string {
	data, err := json.Marshal(value)
	if err != nil {
		return `{"ok":false,"error":{"code":"INTERNAL"}}`
	}
	return string(data)
}

// GetCapabilities returns a safe JSON envelope.
func (b *Binding) GetCapabilities() string {
	return success(capabilitiesDTO(b.manager.GetCapabilities(context.Background())))
}

// CreateSession returns {session_id: ...} in a safe JSON envelope.
func (b *Binding) CreateSession() string {
	id, err := b.manager.CreateSession(context.Background())
	if err != nil {
		return failed(err)
	}
	return success(struct {
		SessionID string `json:"session_id"`
	}{id})
}

// Configure forwards the exact supplied bytes to v1's raw configuration
// parser. The result contains only digest, profile summaries, and warnings.
func (b *Binding) Configure(sessionID, commandID string, rawConfig []byte) string {
	result, err := b.manager.Configure(context.Background(), sessionID, commandID, append([]byte(nil), rawConfig...))
	if err != nil {
		return failed(err)
	}
	return success(configureDTO(result))
}

// Start accepts only v1's stable modes and profile indexes.
func (b *Binding) Start(sessionID, commandID, mode string, index int32) string {
	if index < 0 && mode != string(v1.AutoSelect) {
		return failed(&v1.Error{Code: v1.FailureInvalidArgument})
	}
	result, err := b.manager.Start(context.Background(), sessionID, commandID, v1.StartTarget{Mode: v1.StartMode(mode), Index: int(index)})
	if err != nil {
		return failed(err)
	}
	return success(startDTO(result))
}

func (b *Binding) Stop(sessionID, commandID string, generation int64) string {
	if generation <= 0 {
		return failed(&v1.Error{Code: v1.FailureStaleGeneration})
	}
	result, err := b.manager.Stop(context.Background(), sessionID, commandID, uint64(generation))
	if err != nil {
		return failed(err)
	}
	return success(stopDTO(result))
}

func (b *Binding) Snapshot(sessionID string) string {
	result, err := b.manager.Snapshot(context.Background(), sessionID)
	if err != nil {
		return failed(err)
	}
	return success(snapshotDTO(result))
}

func (b *Binding) Observe(sessionID string, afterSequence int64) string {
	if afterSequence < 0 {
		return failed(&v1.Error{Code: v1.FailureInvalidArgument})
	}
	result, err := b.manager.Observe(context.Background(), sessionID, uint64(afterSequence))
	if err != nil {
		return failed(err)
	}
	return success(observeDTO(result))
}

func (b *Binding) Destroy(sessionID string) string {
	if err := b.manager.DestroySession(context.Background(), sessionID); err != nil {
		return failed(err)
	}
	return success(struct{}{})
}

func generationAsInt64(value uint64) (int64, bool) {
	if value > math.MaxInt64 {
		return 0, false
	}
	return int64(value), true
}

type capabilityDTO struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}
type capabilitiesResultDTO struct {
	Version                  string          `json:"version"`
	Protocols                []string        `json:"protocols"`
	Features                 []capabilityDTO `json:"features"`
	TelemetryNetworkDisabled bool            `json:"telemetry_network_disabled"`
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
type configureResultDTO struct {
	Digest   string       `json:"digest"`
	Profiles []profileDTO `json:"profiles"`
	Warnings []warningDTO `json:"warnings"`
}
type startResultDTO struct {
	Generation int64 `json:"generation"`
}
type stopResultDTO struct {
	Generation int64 `json:"generation"`
}
type snapshotResultDTO struct {
	SessionID       string      `json:"session_id"`
	Generation      int64       `json:"generation"`
	State           string      `json:"state"`
	Configured      bool        `json:"configured"`
	ActiveProfile   *profileDTO `json:"active_profile,omitempty"`
	LastFailure     string      `json:"last_failure,omitempty"`
	CleanupComplete bool        `json:"cleanup_complete"`
}
type eventDTO struct {
	SessionID  string      `json:"session_id"`
	Generation int64       `json:"generation"`
	Sequence   int64       `json:"sequence"`
	State      string      `json:"state"`
	Profile    *profileDTO `json:"profile,omitempty"`
	Failure    string      `json:"failure,omitempty"`
	Warning    *warningDTO `json:"warning,omitempty"`
}
type observeResultDTO struct {
	Events       []eventDTO `json:"events"`
	NextSequence int64      `json:"next_sequence"`
}

func capabilitiesDTO(in v1.Capabilities) capabilitiesResultDTO {
	out := capabilitiesResultDTO{Version: in.Version, TelemetryNetworkDisabled: in.TelemetryNetworkDisabled, Protocols: make([]string, len(in.Protocols)), Features: make([]capabilityDTO, len(in.Features))}
	for i := range in.Protocols {
		out.Protocols[i] = string(in.Protocols[i])
	}
	for i := range in.Features {
		out.Features[i] = capabilityDTO{Name: in.Features[i].Name, Enabled: in.Features[i].Enabled}
	}
	return out
}
func profileResultDTO(in v1.ProfileSummary) profileDTO {
	return profileDTO{Index: int32(in.Index), Protocol: string(in.Protocol), Description: in.Description}
}
func profileResultPtr(in *v1.ProfileSummary) *profileDTO {
	if in == nil {
		return nil
	}
	out := profileResultDTO(*in)
	return &out
}
func configureDTO(in v1.ConfigureResult) configureResultDTO {
	out := configureResultDTO{Digest: in.Digest, Profiles: make([]profileDTO, len(in.Profiles)), Warnings: make([]warningDTO, len(in.Warnings))}
	for i := range in.Profiles {
		out.Profiles[i] = profileResultDTO(in.Profiles[i])
	}
	for i := range in.Warnings {
		out.Warnings[i] = warningDTO{Code: in.Warnings[i].Code, Message: in.Warnings[i].Message}
	}
	return out
}
func startDTO(in v1.StartResult) startResultDTO {
	generation, _ := generationAsInt64(in.Generation)
	return startResultDTO{Generation: generation}
}
func stopDTO(in v1.StopResult) stopResultDTO {
	generation, _ := generationAsInt64(in.Generation)
	return stopResultDTO{Generation: generation}
}
func snapshotDTO(in v1.SnapshotResult) snapshotResultDTO {
	generation, _ := generationAsInt64(in.Generation)
	return snapshotResultDTO{SessionID: in.SessionID, Generation: generation, State: string(in.State), Configured: in.Configured, ActiveProfile: profileResultPtr(in.ActiveProfile), LastFailure: string(in.LastFailure), CleanupComplete: in.CleanupComplete}
}
func observeDTO(in v1.ObserveResult) observeResultDTO {
	next, _ := generationAsInt64(in.NextSequence)
	out := observeResultDTO{Events: make([]eventDTO, len(in.Events)), NextSequence: next}
	for i := range in.Events {
		generation, _ := generationAsInt64(in.Events[i].Generation)
		sequence, _ := generationAsInt64(in.Events[i].Sequence)
		item := eventDTO{SessionID: in.Events[i].SessionID, Generation: generation, Sequence: sequence, State: string(in.Events[i].State), Profile: profileResultPtr(in.Events[i].Profile), Failure: string(in.Events[i].Failure)}
		if in.Events[i].Warning != nil {
			item.Warning = &warningDTO{Code: in.Events[i].Warning.Code, Message: in.Events[i].Warning.Message}
		}
		out.Events[i] = item
	}
	return out
}
