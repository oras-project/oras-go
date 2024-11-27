package progress

import "io"

// TrackReader bind a reader with a tracker.
func TrackReader(t Tracker, r io.Reader) io.ReadCloser {
	return &readTracker{
		base:    r,
		tracker: t,
	}
}

// readTracker tracks the transmission based on the read operation.
type readTracker struct {
	base    io.Reader
	tracker Tracker
	offset  int64
}

// Read reads from the base reader and updates the status.
func (rt *readTracker) Read(p []byte) (n int, err error) {
	n, err = rt.base.Read(p)
	rt.offset += int64(n)
	_ = rt.tracker.Update(Status{
		State:  StateTransmitting,
		Offset: rt.offset,
	})
	if err != nil && err != io.EOF {
		_ = rt.tracker.Fail(err)
	}
	return n, err
}

// Close closes the tracker.
func (rt *readTracker) Close() error {
	return rt.tracker.Close()
}
