package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"
)

// MobileTransport is the narrow native boundary for the mobile UI. The
// implementation may be a gomobile call in a platform process or a native
// app/extension bridge; the UI never owns a second session manager.
type MobileTransport interface {
	Configure(string, int64, []byte) string
	Start(string, int64, string, int32) string
	Stop(string, int64) string
	Snapshot(string) string
	Reset(string, int64) string
}

type MobileClient struct {
	api MobileTransport

	mu        sync.RWMutex
	sessionID string
}

// NewMobileClientWithTransport is used by platform entry points and by tests.
// It keeps the shared UI independent from Android/iOS imports.
func NewMobileClientWithTransport(transport MobileTransport) *MobileClient {
	if transport == nil {
		panic("nil mobile transport")
	}
	return &MobileClient{api: transport}
}

func (c *MobileClient) session() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.sessionID
}

func (c *MobileClient) remember(id string) {
	if id == "" {
		return
	}
	c.mu.Lock()
	c.sessionID = id
	c.mu.Unlock()
}

func (c *MobileClient) clear() {
	c.mu.Lock()
	c.sessionID = ""
	c.mu.Unlock()
}

// invoke gives native transports a cancellation boundary. The underlying
// gomobile/JNI call cannot always be interrupted, but the UI never waits past
// its caller's context and a subsequent Snapshot can reattach after a native
// process restart.
func invoke(ctx context.Context, call func() string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := contextError(ctx); err != nil {
		return "", err
	}
	result := make(chan string, 1)
	go func() { result <- call() }()
	select {
	case raw := <-result:
		return raw, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (c *MobileClient) Configure(ctx context.Context, raw []byte, sequence uint64) (ConfigureResult, error) {
	if err := contextError(ctx); err != nil {
		return ConfigureResult{}, err
	}
	transportSequence, err := mobileInt64(sequence, "sequence")
	if err != nil {
		return ConfigureResult{}, err
	}
	rawResult, err := invoke(ctx, func() string {
		return c.api.Configure(c.session(), transportSequence, append([]byte(nil), raw...))
	})
	if err != nil {
		return ConfigureResult{}, err
	}
	value, err := decodeEnvelope(rawResult)
	if err != nil {
		c.clearOnTransportFailure(err)
		return ConfigureResult{}, err
	}
	var result configureDTO
	if err := json.Unmarshal(value, &result); err != nil {
		return ConfigureResult{}, fmt.Errorf("decode mobile configure result: %w", err)
	}
	return ConfigureResult{Digest: result.Digest, Sequence: result.Sequence, SourceKind: result.SourceKind, Profiles: result.Profiles.ui(), Warnings: result.Warnings.ui()}, nil
}

func (c *MobileClient) Start(ctx context.Context, sequence uint64) (StartResult, error) {
	if err := contextError(ctx); err != nil {
		return StartResult{}, err
	}
	transportSequence, err := mobileInt64(sequence, "sequence")
	if err != nil {
		return StartResult{}, err
	}
	rawResult, err := invoke(ctx, func() string {
		// AUTO_SELECT is represented by index zero by the native providers. A
		// negative sentinel was accepted by the Go manager but rejected by the
		// Swift bridge, creating a platform-only failure.
		return c.api.Start(c.session(), transportSequence, "AUTO_SELECT", 0)
	})
	if err != nil {
		return StartResult{}, err
	}
	value, err := decodeEnvelope(rawResult)
	if err != nil {
		c.clearOnTransportFailure(err)
		return StartResult{}, err
	}
	var result startDTO
	if err := json.Unmarshal(value, &result); err != nil {
		return StartResult{}, fmt.Errorf("decode mobile start result: %w", err)
	}
	return StartResult(result), nil
}

func (c *MobileClient) Stop(ctx context.Context, generation uint64) (StopResult, error) {
	if err := contextError(ctx); err != nil {
		return StopResult{}, err
	}
	transportGeneration, err := mobileInt64(generation, "generation")
	if err != nil {
		return StopResult{}, err
	}
	rawResult, err := invoke(ctx, func() string { return c.api.Stop(c.session(), transportGeneration) })
	if err != nil {
		return StopResult{}, err
	}
	value, err := decodeEnvelope(rawResult)
	if err != nil {
		c.clearOnTransportFailure(err)
		return StopResult{}, err
	}
	var result stopDTO
	if err := json.Unmarshal(value, &result); err != nil {
		return StopResult{}, fmt.Errorf("decode mobile stop result: %w", err)
	}
	return StopResult(result), nil
}

func (c *MobileClient) Snapshot(ctx context.Context) (Snapshot, error) {
	if err := contextError(ctx); err != nil {
		return Snapshot{}, err
	}
	rawResult, err := invoke(ctx, func() string { return c.api.Snapshot(c.session()) })
	if err != nil {
		return Snapshot{}, err
	}
	value, err := decodeEnvelope(rawResult)
	if err != nil {
		c.clearOnTransportFailure(err)
		return Snapshot{}, err
	}
	var result snapshotDTO
	if err := json.Unmarshal(value, &result); err != nil {
		return Snapshot{}, fmt.Errorf("decode mobile snapshot: %w", err)
	}
	c.remember(result.SessionID)
	return result.ui(), nil
}

// Watch uses a bounded, coalesced refresh for the mobile activity. The native
// service/extension remains the lifecycle owner; this refresh is only the
// foreground view's wake mechanism and stops immediately with its context.
func (c *MobileClient) Watch(ctx context.Context) (<-chan Snapshot, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	updates := make(chan Snapshot, 1)
	go func() {
		defer close(updates)
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			snapshot, err := c.Snapshot(ctx)
			switch {
			case err == nil:
				select {
				case updates <- snapshot:
				default:
					select {
					case <-updates:
					case <-ctx.Done():
						return
					}
					select {
					case updates <- snapshot:
					case <-ctx.Done():
						return
					}
				}
			case ctx.Err() != nil:
				return
			default:
				// Closing the stream tells ConnectionView to enter its bounded
				// reconnect path instead of silently presenting stale state.
				return
			}
			select {
			case <-ticker.C:
			case <-ctx.Done():
				return
			}
		}
	}()
	return updates, nil
}

func (c *MobileClient) Reset(ctx context.Context, sequence uint64) (Snapshot, error) {
	if err := contextError(ctx); err != nil {
		return Snapshot{}, err
	}
	transportSequence, err := mobileInt64(sequence, "sequence")
	if err != nil {
		return Snapshot{}, err
	}
	rawResult, err := invoke(ctx, func() string { return c.api.Reset(c.session(), transportSequence) })
	if err != nil {
		return Snapshot{}, err
	}
	value, err := decodeEnvelope(rawResult)
	if err != nil {
		c.clearOnTransportFailure(err)
		return Snapshot{}, err
	}
	var result snapshotDTO
	if err := json.Unmarshal(value, &result); err != nil {
		return Snapshot{}, fmt.Errorf("decode mobile reset result: %w", err)
	}
	c.remember(result.SessionID)
	return result.ui(), nil
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

func mobileInt64(value uint64, name string) (int64, error) {
	converted, err := strconv.ParseInt(strconv.FormatUint(value, 10), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s is outside the mobile transport range: %w", name, err)
	}
	return converted, nil
}

type mobileEnvelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result"`
	Error  *mobileError    `json:"error"`
}
type mobileError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type mobileResponseError struct {
	code    string
	message string
}

func (e *mobileResponseError) Error() string {
	if e.message == "" {
		return e.code
	}
	return e.code + ": " + e.message
}

func (c *MobileClient) clearOnTransportFailure(err error) {
	var responseErr *mobileResponseError
	if errors.As(err, &responseErr) {
		// A stale owner is recoverable by a fresh empty-session Snapshot. Other
		// domain errors retain the owner so the UI can show the authoritative
		// failure and retry without losing accepted configuration.
		switch responseErr.code {
		case "INTERNAL", "SESSION_NOT_FOUND", "STALE_SESSION", "PLATFORM_FAILED":
			c.clear()
		}
	}
}

func decodeEnvelope(raw string) (json.RawMessage, error) {
	var envelope mobileEnvelope
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		return nil, fmt.Errorf("decode mobile response: %w", err)
	}
	if !envelope.OK {
		if envelope.Error == nil {
			return nil, &mobileResponseError{code: "INTERNAL", message: "mobile session request failed"}
		}
		return nil, &mobileResponseError{code: envelope.Error.Code, message: envelope.Error.Message}
	}
	return envelope.Result, nil
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
type profilesDTO []profileDTO
type warningsDTO []warningDTO
type failureDTO struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
type configureDTO struct {
	Digest     string      `json:"digest"`
	Sequence   uint64      `json:"sequence"`
	SourceKind string      `json:"source_kind"`
	Profiles   profilesDTO `json:"profiles"`
	Warnings   warningsDTO `json:"warnings"`
}
type startDTO struct {
	Generation uint64 `json:"generation"`
	Sequence   uint64 `json:"sequence"`
}
type stopDTO startDTO
type snapshotDTO struct {
	SessionID       string      `json:"session_id"`
	Sequence        uint64      `json:"sequence"`
	Generation      uint64      `json:"generation"`
	State           string      `json:"state"`
	Configured      bool        `json:"configured"`
	Digest          string      `json:"digest"`
	SourceKind      string      `json:"source_kind"`
	Profiles        profilesDTO `json:"profiles"`
	Warnings        warningsDTO `json:"warnings"`
	ActiveProfile   *profileDTO `json:"active_profile"`
	LastFailure     *failureDTO `json:"last_failure"`
	CleanupComplete bool        `json:"cleanup_complete"`
	Recovering      bool        `json:"recovering"`
}

func (values profilesDTO) ui() []Profile {
	result := make([]Profile, len(values))
	for i, value := range values {
		result[i] = Profile{Index: value.Index, Protocol: Protocol(value.Protocol), Description: value.Description}
	}
	return result
}

func (values warningsDTO) ui() []Warning {
	result := make([]Warning, len(values))
	for i, value := range values {
		result[i] = Warning(value)
	}
	return result
}

func (value snapshotDTO) ui() Snapshot {
	result := Snapshot{SessionID: value.SessionID, Sequence: value.Sequence, Generation: value.Generation, State: State(value.State), Configured: value.Configured, Digest: value.Digest, SourceKind: value.SourceKind, Profiles: value.Profiles.ui(), Warnings: value.Warnings.ui(), CleanupDone: value.CleanupComplete, Recovering: value.Recovering}
	if value.ActiveProfile != nil {
		converted := value.ActiveProfile.ui()
		result.ActiveProfile = &converted
	}
	if value.LastFailure != nil {
		result.LastFailure = &Failure{Code: value.LastFailure.Code, Message: value.LastFailure.Message}
	}
	return result
}

func (value profileDTO) ui() Profile {
	return Profile{Index: value.Index, Protocol: Protocol(value.Protocol), Description: value.Description}
}
