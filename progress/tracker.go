package progress

import "io"

// Tracker updates the status of a descriptor.
type Tracker interface {
	io.Closer

	// Update updates the status of the descriptor.
	Update(status Status) error

	// Fail marks the descriptor as failed.
	Fail(err error) error
}

// Start starts tracking the transmission.
func Start(t Tracker) error {
	return t.Update(Status{
		State:  StateInitialized,
		Offset: -1,
	})
}

// Done marks the transmission as complete.
// Done should be called after the transmission is complete.
// Note: Reading all content from the reader does not imply the transmission is
// complete.
func Done(t Tracker) error {
	return t.Update(Status{
		State:  StateTransmitted,
		Offset: -1,
	})
}
