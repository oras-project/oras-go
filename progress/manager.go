package progress

import (
	"io"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// Manager tracks the progress of multiple descriptors.
type Manager interface {
	io.Closer

	// Track starts tracking the progress of a descriptor.
	Track(desc ocispec.Descriptor) (Tracker, error)
}

// ManagerFunc is an adapter to allow the use of ordinary functions as Managers.
// If f is a function with the appropriate signature, ManagerFunc(f) is a
// [Manager] that calls f.
type ManagerFunc func(ocispec.Descriptor, Status, error) error

// Track starts tracking the progress of a descriptor.
func (f ManagerFunc) Track(desc ocispec.Descriptor) (Tracker, error) {
	return TrackerFunc(func(status Status, err error) error {
		return f(desc, status, err)
	}), nil
}

// Close closes the manager.
func (f ManagerFunc) Close() error {
	return nil
}

// Record adds the progress of a descriptor as a single entry.
func Record(m Manager, desc ocispec.Descriptor, status Status) error {
	tracker, err := m.Track(desc)
	if err != nil {
		return err
	}
	err = tracker.Update(status)
	if err != nil {
		return err
	}
	return tracker.Close()
}
