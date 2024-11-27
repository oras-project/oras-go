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
