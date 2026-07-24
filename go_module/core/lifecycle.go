package core

import "fmt"

// LifecycleState is the authoritative state of one CoreClient generation.
// Exported values make platform shells able to distinguish an in-progress stop
// from a disconnected client without inferring it from logs or health probes.
type LifecycleState string

const (
	StateIdle      LifecycleState = "IDLE"
	StatePreparing LifecycleState = "PREPARING"
	StateProbing   LifecycleState = "PROBING"
	StateConnected LifecycleState = "CONNECTED"
	StateStopping  LifecycleState = "STOPPING"
	StateFailed    LifecycleState = "FAILED"
)

func (s LifecycleState) String() string { return string(s) }

func lifecycleBusyError(state LifecycleState) error {
	return fmt.Errorf("core client lifecycle is %s", state)
}
