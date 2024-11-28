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

// TrackerFunc is an adapter to allow the use of ordinary functions as Trackers.
// If f is a function with the appropriate signature, TrackerFunc(f) is a
// [Tracker] that calls f.
type TrackerFunc func(Status, error) error

// Update updates the status of the descriptor.
func (f TrackerFunc) Update(status Status) error {
	return f(status, nil)
}

// Fail marks the descriptor as failed.
func (f TrackerFunc) Fail(err error) error {
	return f(Status{}, err)
}

// Close closes the tracker.
func (f TrackerFunc) Close() error {
	return nil
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

// TrackReader bind a reader with a tracker.
func TrackReader(t Tracker, r io.Reader) io.ReadCloser {
	rt := readTracker{
		base:    r,
		tracker: t,
	}
	if _, ok := r.(io.WriterTo); ok {
		return &readTrackerWriteTo{rt}
	}
	return &rt
}

// readTracker tracks the transmission based on the read operation.
type readTracker struct {
	base    io.Reader
	tracker Tracker
	offset  int64
}

// Read reads from the base reader and updates the status.
func (rt *readTracker) Read(p []byte) (int, error) {
	n, err := rt.base.Read(p)
	rt.offset += int64(n)
	if n > 0 {
		_ = rt.tracker.Update(Status{
			State:  StateTransmitting,
			Offset: rt.offset,
		})
	}
	if err != nil && err != io.EOF {
		_ = rt.tracker.Fail(err)
	}
	return n, err
}

// Close closes the tracker.
func (rt *readTracker) Close() error {
	return rt.tracker.Close()
}

// readTrackerWriteTo is readTracker with WriteTo support.
type readTrackerWriteTo struct {
	readTracker
}

// WriteTo writes to the base writer and updates the status.
func (rt *readTrackerWriteTo) WriteTo(w io.Writer) (int64, error) {
	wt := &writeTracker{
		base:    w,
		tracker: rt.tracker,
		offset:  rt.offset,
	}
	n, err := rt.base.(io.WriterTo).WriteTo(wt)
	rt.offset = wt.offset
	return n, err
}

// writeTracker tracks the transmission based on the write operation.
type writeTracker struct {
	base    io.Writer
	tracker Tracker
	offset  int64
}

// Write writes to the base writer and updates the status.
func (wt *writeTracker) Write(p []byte) (int, error) {
	n, err := wt.base.Write(p)
	wt.offset += int64(n)
	if n > 0 {
		_ = wt.tracker.Update(Status{
			State:  StateTransmitting,
			Offset: wt.offset,
		})
	}
	if err != nil {
		_ = wt.tracker.Fail(err)
	}
	return n, err
}
