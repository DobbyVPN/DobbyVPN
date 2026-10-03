// Package mobilebinding exposes the session API through gomobile-safe values.
//
// It serializes public state DTOs and complete failure messages. Configuration
// bytes are accepted at the edge and never returned on a successful result.
package mobilebinding

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"core/sessionapi"
	"core/sessionapi/wire"
)

// PlatformCallbacks is implemented by the Android service or iOS extension
// shell. PublishState synchronizes native tunnel lifecycle state; UI clients
// independently read Snapshot as the authoritative state. Every callback
// carries the owner and generation so a delayed platform result cannot affect
// another connection attempt.
// AcquireTunnel must return a fresh duplicated descriptor owned by Go. Go
// closes that descriptor before ReleaseTunnel; a failed release prevents Go
// from reporting cleanup as complete.
type PlatformCallbacks interface {
	AcquireTunnel(sessionID string, generation int64) string
	ReleaseTunnel(sessionID string, generation int64, fd int32, timeoutMillis int64) string
	ProtectSocket(sessionID string, generation int64, fd int32) bool
	PublishState(sessionID string, generation int64, state string, failureCode string)
}

// SourceCallbacks is used by iOS, whose provider cannot write the app's URL
// file directly. Android and desktop use the shared Go file store.
type SourceCallbacks interface {
	LoadSourceURL() string
	SaveSourceURL(value string) bool
	ClearSourceURL() bool
}

type managerAPI interface {
	Configure(context.Context, string, uint64, []byte) (sessionapi.ConfigureResult, error)
	Start(context.Context, string, uint64, sessionapi.StartTarget) (sessionapi.StartResult, error)
	Stop(context.Context, string, uint64) (sessionapi.StopResult, error)
	Snapshot(context.Context, string) (sessionapi.SnapshotResult, error)
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

// NewForDesktop wraps the process-owned manager used by the desktop Go backend.
func NewForDesktop(manager *sessionapi.Manager) *Binding {
	return &Binding{manager: manager}
}

// StopAndWait is reserved for OS lifecycle callbacks; CallJSON deliberately
// exposes only the four existing desktop methods.
func (b *Binding) StopAndWait(ctx context.Context) error {
	owner, ok := b.manager.(interface{ StopAndWait(context.Context) error })
	if !ok {
		return &sessionapi.Error{Code: sessionapi.FailureUnsupported, Message: "shutdown owner is unavailable"}
	}
	return owner.StopAndWait(ctx)
}

func (b *Binding) Resume() error {
	owner, ok := b.manager.(interface{ Resume() error })
	if !ok {
		return &sessionapi.Error{Code: sessionapi.FailureUnsupported, Message: "session owner is unavailable"}
	}
	return owner.Resume()
}

func (b *Binding) AttachSourceStore() {
	manager, ok := b.manager.(interface {
		AttachSourceStore(context.Context, sessionapi.SourceStore) error
	})
	store, storeOK := b.platform.(sessionapi.SourceStore)
	if ok && storeOK {
		_ = manager.AttachSourceStore(context.Background(), store)
	}
}

func (b *Binding) AttachFileSourceStore(path string) error {
	if path == "" {
		return &sessionapi.Error{Code: sessionapi.FailureInvalidArgument, Message: "saved source path is empty"}
	}
	manager, ok := b.manager.(interface {
		AttachSourceStore(context.Context, sessionapi.SourceStore) error
	})
	if !ok {
		return &sessionapi.Error{Code: sessionapi.FailureUnsupported, Message: "source storage is unavailable"}
	}
	return manager.AttachSourceStore(context.Background(), sessionapi.FileSourceStore{Path: path})
}

func success(value interface{}) string { return encode(wire.Response[any]{OK: true, Result: value}) }
func failed(err error) string {
	message := err.Error()
	var domain *sessionapi.Error
	if errors.As(err, &domain) {
		message = domain.Message
		if domain.Cause != nil {
			message += ": " + domain.Cause.Error()
		}
	}
	return encode(wire.Response[any]{OK: false, Error: &wire.Failure{Code: string(sessionapi.CodeOf(err)), Message: message}})
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
	return b.ConfigureContext(context.Background(), sessionID, expectedSequence, rawConfig)
}

func (b *Binding) ConfigureContext(ctx context.Context, sessionID string, expectedSequence int64, rawConfig []byte) string {
	sequence, err := nonNegative(expectedSequence, "configuration sequence")
	if err != nil {
		return failed(err)
	}
	result, err := b.manager.Configure(ctx, sessionID, sequence, append([]byte(nil), rawConfig...))
	if err != nil {
		return failed(err)
	}
	return success(wire.ConfigurationFrom(result))
}

// Start starts an attempt only if the inspected snapshot is still current.
func (b *Binding) Start(sessionID string, expectedSequence int64, mode string, index int32) string {
	return b.StartContext(context.Background(), sessionID, expectedSequence, mode, index)
}

func (b *Binding) StartContext(ctx context.Context, sessionID string, expectedSequence int64, mode string, index int32) string {
	return b.StartWithSourceContext(ctx, sessionID, expectedSequence, mode, index, nil)
}

// StartWithSource accepts a changed configuration and starts it in one backend
// operation. A nil source reuses the accepted configuration.
func (b *Binding) StartWithSource(sessionID string, expectedSequence int64, mode string, index int32, rawConfig []byte) string {
	return b.StartWithSourceContext(context.Background(), sessionID, expectedSequence, mode, index, rawConfig)
}

func (b *Binding) StartWithSourceContext(ctx context.Context, sessionID string, expectedSequence int64, mode string, index int32, rawConfig []byte) string {
	sequence, err := nonNegative(expectedSequence, "start sequence")
	if err != nil {
		return failed(err)
	}
	if index < 0 && mode != string(sessionapi.AutoSelect) {
		return failed(&sessionapi.Error{Code: sessionapi.FailureInvalidArgument, Message: "profile index must be non-negative"})
	}
	result, err := b.manager.Start(ctx, sessionID, sequence, sessionapi.StartTarget{
		Mode: sessionapi.StartMode(mode), Index: int(index), Source: bytes.Clone(rawConfig),
	})
	if err != nil {
		return failed(err)
	}
	return success(wire.Generation{Generation: result.Generation, Sequence: result.Sequence})
}

// Stop stops only the requested generation. Repeating a completed stop is
// harmless according to the manager contract.
func (b *Binding) Stop(sessionID string, generation int64) string {
	return b.StopContext(context.Background(), sessionID, generation)
}

func (b *Binding) StopContext(ctx context.Context, sessionID string, generation int64) string {
	if generation <= 0 {
		return failed(&sessionapi.Error{Code: sessionapi.FailureStaleGeneration, Message: "generation must be positive"})
	}
	result, err := b.manager.Stop(ctx, sessionID, uint64(generation))
	if err != nil {
		return failed(err)
	}
	return success(wire.Generation{Generation: result.Generation, Sequence: result.Sequence})
}

// Snapshot attaches to the process-owned session when sessionID is empty.
func (b *Binding) Snapshot(sessionID string) string {
	return b.SnapshotContext(context.Background(), sessionID)
}

func (b *Binding) SnapshotContext(ctx context.Context, sessionID string) string {
	result, err := b.manager.Snapshot(ctx, sessionID)
	if err != nil {
		return failed(err)
	}
	return success(wire.SnapshotFrom(result))
}

// CallJSON is the shared desktop/iOS command dispatcher. Android's gomobile
// methods continue to use the same typed methods and DTO conversions above.
func (b *Binding) CallJSON(ctx context.Context, method string, params json.RawMessage) string {
	return b.CallJSONWithConfiguration(ctx, method, params, nil, false)
}

// CallJSONWithConfiguration dispatches the same method/params request used by
// desktop control. iOS supplies configuration bytes separately because its
// NetworkExtension message channel must not carry profile secrets. The
// request parameters remain the shared JSON command; iOS supplies a changed
// Start or Configure source through the platform-owned secret mailbox.
func (b *Binding) CallJSONWithConfiguration(
	ctx context.Context,
	method string,
	params json.RawMessage,
	rawConfiguration []byte,
	hasConfiguration bool,
) string {
	var value struct {
		SessionID        string  `json:"session_id"`
		ExpectedSequence int64   `json:"expected_sequence"`
		Generation       int64   `json:"generation"`
		Source           *string `json:"source"`
		Mode             string  `json:"mode"`
		Index            int32   `json:"index"`
	}
	if len(params) != 0 {
		decoder := json.NewDecoder(bytes.NewReader(params))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&value); err != nil {
			return failed(&sessionapi.Error{Code: sessionapi.FailureInvalidArgument, Message: "command parameters are invalid", Cause: err})
		}
	}
	switch method {
	case "Snapshot":
		return b.SnapshotContext(ctx, value.SessionID)
	case "Configure":
		if hasConfiguration {
			return b.ConfigureContext(ctx, value.SessionID, value.ExpectedSequence, rawConfiguration)
		}
		if value.Source == nil {
			return b.ConfigureContext(ctx, value.SessionID, value.ExpectedSequence, nil)
		}
		return b.ConfigureContext(ctx, value.SessionID, value.ExpectedSequence, []byte(*value.Source))
	case "Start":
		if hasConfiguration {
			if rawConfiguration == nil {
				rawConfiguration = []byte{}
			}
			return b.StartWithSourceContext(ctx, value.SessionID, value.ExpectedSequence, value.Mode, value.Index, rawConfiguration)
		}
		if value.Source != nil {
			return b.StartWithSourceContext(ctx, value.SessionID, value.ExpectedSequence, value.Mode, value.Index, []byte(*value.Source))
		}
		return b.StartContext(ctx, value.SessionID, value.ExpectedSequence, value.Mode, value.Index)
	case "Stop":
		return b.StopContext(ctx, value.SessionID, value.Generation)
	default:
		return failed(&sessionapi.Error{Code: sessionapi.FailureInvalidArgument, Message: "unknown command"})
	}
}

func nonNegative(value int64, name string) (uint64, error) {
	if value < 0 {
		return 0, &sessionapi.Error{Code: sessionapi.FailureInvalidArgument, Message: name + " must be non-negative"}
	}
	return uint64(value), nil
}
