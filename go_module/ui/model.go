// Package ui contains the shared Go user interface and its transport-neutral
// client boundary.  The UI deliberately consumes snapshots instead of owning
// VPN state; the service process remains the single session owner.
package ui

import "context"

// State is the stable presentation vocabulary shared with sessionapi.
type State string

const (
	StateIdle       State = "IDLE"
	StateConfigured State = "CONFIGURED"
	StateProbing    State = "PROBING"
	StatePreparing  State = "PREPARING"
	StateConnected  State = "CONNECTED"
	StateStopping   State = "STOPPING"
	StateFailed     State = "FAILED"
)

type Protocol string

const (
	ProtocolOutline     Protocol = "OUTLINE"
	ProtocolXray        Protocol = "XRAY"
	ProtocolTrustTunnel Protocol = "TRUST_TUNNEL"
)

type Profile struct {
	Index       int32
	Protocol    Protocol
	Description string
}

type Warning struct {
	Code    string
	Message string
}

type Failure struct {
	Code    string
	Message string
}

type Snapshot struct {
	SessionID     string
	Sequence      uint64
	Generation    uint64
	State         State
	Configured    bool
	Digest        string
	SourceKind    string
	Profiles      []Profile
	Warnings      []Warning
	ActiveProfile *Profile
	LastFailure   *Failure
	CleanupDone   bool
	Recovering    bool
}

type ConfigureResult struct {
	Digest     string
	Sequence   uint64
	SourceKind string
	Profiles   []Profile
	Warnings   []Warning
}

type StartResult struct {
	Generation uint64
	Sequence   uint64
}

type StopResult struct {
	Generation uint64
	Sequence   uint64
}

// SessionClient is the only service boundary the UI knows about.  The
// concrete desktop implementation is authenticated gRPC; mobile shells can
// adapt the same interface to their native Go binding without changing the UI.
type SessionClient interface {
	Configure(context.Context, []byte, uint64) (ConfigureResult, error)
	Start(context.Context, uint64) (StartResult, error)
	Stop(context.Context, uint64) (StopResult, error)
	Snapshot(context.Context) (Snapshot, error)
	Watch(context.Context) (<-chan Snapshot, error)
	Reset(context.Context, uint64) (Snapshot, error)
}

// LogExporter is the single UI boundary for an explicit diagnostic export.
// The UI supplies retained diagnostic lines from its trusted DiagnosticStore;
// a platform adapter owns the save/share mechanics and any archive format
// required by that platform. It never receives the entered configuration
// source.
type LogExporter interface {
	Export(context.Context, []string) error
}
