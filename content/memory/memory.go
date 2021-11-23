package memory

import (
	"context"
	"fmt"
	"io"
	"sync"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/internal/cas"
	"oras.land/oras-go/v2/internal/descriptor"
	"oras.land/oras-go/v2/internal/resolver"
)

// Store represents a memory based store, which implements `oras.Target`.
type Store struct {
	storage  content.Storage
	resolver content.TagResolver
	upEdges  sync.Map // map[descriptor.Descriptor]map[descriptor.Descriptor]ocispec.Descriptor
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
func (s *Store) Push(ctx context.Context, expected ocispec.Descriptor, reader io.Reader) error {
	if err := s.storage.Push(ctx, expected, reader); err != nil {
		return err
	}

	// index up edges.
	// there is no data consistency issue as long as deletion is not implemented
	// for the memory store.
	upEdgeKey := descriptor.FromOCI(expected)
	downEdges, err := content.DownEdges(ctx, s.storage, expected)
	if err != nil {
		return err
	}
	for _, downEdge := range downEdges {
		downEdgeKey := descriptor.FromOCI(downEdge)
		upEdgesValue, _ := s.upEdges.LoadOrStore(downEdgeKey, &sync.Map{})
		upEdges := upEdgesValue.(*sync.Map)
		upEdges.Store(upEdgeKey, expected)
	}
	return nil
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

// UpEdges returns the nodes directly pointing to the current node.
// UpEdges returns nil without error if the node does not exists in the store.
// Like other operations, calling UpEdges() is go-routine safe. However, it does
// not necessarily correspond to any consistent snapshot of the stored contents.
func (s *Store) UpEdges(_ context.Context, node ocispec.Descriptor) ([]ocispec.Descriptor, error) {
	key := descriptor.FromOCI(node)
	upEdgesValue, exists := s.upEdges.Load(key)
	if !exists {
		return nil, nil
	}
	upEdges := upEdgesValue.(*sync.Map)

	var res []ocispec.Descriptor
	upEdges.Range(func(key, value interface{}) bool {
		res = append(res, value.(ocispec.Descriptor))
		return true
	})
	return res, nil
}
