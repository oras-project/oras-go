package progress

// State represents the state of a descriptor.
type State int

// Registered states.
const (
	StateUnknown      State = iota // unknown state
	StateInitialized               // progress initialized
	StateTransmitting              // transmitting content
	StateTransmitted               // content transmitted
	StateExists                    // content exists
	StateSkipped                   // content skipped
	StateMounted                   // content mounted
)

// Status represents the status of a descriptor.
type Status struct {
	// State represents the state of the descriptor.
	State State

	// Offset represents the current offset of the descriptor.
	// Offset is discarded if set to a negative value.
	Offset int64
}
