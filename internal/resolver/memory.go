package resolver

import (
	"context"
	"sync"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/errdef"
)

// Memory is a memory based resolver.
type Memory struct {
	index sync.Map // map[string]ocispec.Descriptor
}

// NewMemory creates a new Memory resolver.
func NewMemory() *Memory {
	return &Memory{}
}

// Resolve resolves a reference to a descriptor.
func (m *Memory) Resolve(_ context.Context, reference string) (ocispec.Descriptor, error) {
	desc, ok := m.index.Load(reference)
	if !ok {
		return ocispec.Descriptor{}, errdef.ErrNotFound
	}
	return desc.(ocispec.Descriptor), nil
}

// Tag tags a descriptor with a reference string.
func (m *Memory) Tag(_ context.Context, desc ocispec.Descriptor, reference string) error {
	m.index.Store(reference, desc)
	return nil
}

// Map dumps the memory into a built-in map structure.
// Like other operations, calling Map() is go-routine safe. However, it does not
// necessarily correspond to any consistent snapshot of the storage contents.
func (m *Memory) Map() map[string]ocispec.Descriptor {
	res := make(map[string]ocispec.Descriptor)
	m.index.Range(func(key, value interface{}) bool {
		res[key.(string)] = value.(ocispec.Descriptor)
		return true
	})
	return res
}
