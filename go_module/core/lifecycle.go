package core

import "fmt"

// Runtime is the narrow native resource boundary consumed by
// sessionapi/runtime. SessionV2 owns session state and generations; callers
// can only connect and disconnect this resource adapter.
type Runtime interface {
	Connect() error
	Disconnect() error
}

// lifecycleState is the internal phase of one native resource adapter. It is
// not a public session state; SessionV2 owns the externally meaningful state
// and generation contract.
type lifecycleState string

const (
	stateIdle      lifecycleState = "IDLE"
	statePreparing lifecycleState = "PREPARING"
	stateProbing   lifecycleState = "PROBING"
	stateConnected lifecycleState = "CONNECTED"
	stateStopping  lifecycleState = "STOPPING"
	stateFailed    lifecycleState = "FAILED"
)

func (s lifecycleState) String() string { return string(s) }

func lifecycleBusyError(state lifecycleState) error {
	return fmt.Errorf("native session runtime lifecycle is %s", state)
}
