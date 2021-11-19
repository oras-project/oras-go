package memory

import (
	"context"
	"fmt"
	"io"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/content"
	"oras.land/oras-go/errdef"
	"oras.land/oras-go/internal/cas"
	"oras.land/oras-go/internal/resolver"
)

// Store represents a memory based store, which implements `oras.Target`.
type Store struct {
	storage  *cas.Memory
	resolver content.TagResolver
}

// New creates a new memory based store.
func New() *Store {
	return &Store{
		storage:  cas.NewMemory(),
		resolver: resolver.NewMemory(),
	}
}

// Fetch fetches the content identified by the descriptor.
func (s *Store) Fetch(ctx context.Context, target ocispec.Descriptor) (io.ReadCloser, error) {
	return s.storage.Fetch(ctx, target)
}

// Push pushes the content, matching the expected descriptor.
func (s *Store) Push(ctx context.Context, expected ocispec.Descriptor, content io.Reader) error {
	return s.storage.Push(ctx, expected, content)
}

// Exists returns true if the described content exists.
func (s *Store) Exists(ctx context.Context, target ocispec.Descriptor) (bool, error) {
	return s.storage.Exists(ctx, target)
}

// Resolve resolves a reference to a descriptor.
func (s *Store) Resolve(ctx context.Context, reference string) (ocispec.Descriptor, error) {
	return s.resolver.Resolve(ctx, reference)
}

// Tag tags a descriptor with a reference string.
// Returns ErrNotFound if the tagged content does not exist.
func (s *Store) Tag(ctx context.Context, desc ocispec.Descriptor, reference string) error {
	exists, err := s.storage.Exists(ctx, desc)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("%s: %s: %w", desc.Digest, desc.MediaType, errdef.ErrNotFound)
	}
	return s.resolver.Tag(ctx, desc, reference)
}

// Put stores the content of the specified media type in the memory.
// A descriptor is returned for describing the given content.
func (s *Store) Put(mediaType string, content []byte) (ocispec.Descriptor, error) {
	return s.storage.Put(mediaType, content)
}
