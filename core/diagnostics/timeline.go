package diagnostics

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"sync/atomic"
	"time"
)

// Lifecycle stage records are ordinary synchronized log records: each one is
// written by core/log into the retained log of its own process, so the logging
// contract's four-file budget (two destinations, each keeping its current and
// previous generation) is unchanged by them.
const (
	// TimelineEvent marks every stage record in the structured log.
	TimelineEvent = "lifecycle.stage"
	// TimelineCategory groups the stage records.
	TimelineCategory = "TIMELINE"
)

// Process roles. These name who is speaking, never a machine or device.
const (
	RoleBackend   = "backend"
	RoleCLI       = "cli"
	RoleIOSApp    = "ios-app"
	RoleIOSTunnel = "ios-tunnel"
	RoleAndroid   = "android-app"
)

var (
	currentAttempt atomic.Pointer[string]
	currentRole    atomic.Pointer[string]
)

// SetRole records which process is writing. Producers set it once at startup.
func SetRole(role string) {
	value := role
	currentRole.Store(&value)
}

// Role reports the installed process role, or "" when unconfigured.
func Role() string {
	value := currentRole.Load()
	if value == nil {
		return ""
	}
	return *value
}

// NewAttemptID returns 32 lowercase hex characters, one per start attempt. It
// is never persisted outside its own stage records.
func NewAttemptID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		// The runtime's CSPRNG does not fail on the supported platforms; keep a
		// distinct value rather than an empty correlation field.
		return hex.EncodeToString([]byte(time.Now().Format(time.RFC3339Nano)))
	}
	return hex.EncodeToString(raw[:])
}

// SetAttemptID stores the attempt the current process is serving.
func SetAttemptID(id string) {
	value := id
	currentAttempt.Store(&value)
}

// CurrentAttemptID reports the stored attempt. A producer that never minted one
// gets a per-process identity, so no stage record is ever written without a
// correlation field.
func CurrentAttemptID() string {
	if value := currentAttempt.Load(); value != nil && *value != "" {
		return *value
	}
	return processAttempt()
}

var fallbackAttempt struct {
	once sync.Once
	id   string
}

// processAttempt returns one stable identity for producers that never mint a
// per-attempt ID, so every stage record stays correlatable.
func processAttempt() string {
	fallbackAttempt.once.Do(func() { fallbackAttempt.id = NewAttemptID() })
	return fallbackAttempt.id
}
