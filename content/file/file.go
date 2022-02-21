/*
Copyright The ORAS Authors.
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package file

import (
	"context"
	"fmt"
	"io"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/internal/graph"
	"oras.land/oras-go/v2/internal/resolver"
)

// Store represents a file system based store, which implements `oras.Target`.
type Store struct {
	*storage
	resolver *resolver.Memory
	graph    *graph.Memory
}

// New creates a new file store.
func New(workingDir string) *Store {
	store := &Store{
		storage:  newStorage(workingDir),
		resolver: resolver.NewMemory(),
		graph:    graph.NewMemory(),
	}
	return store
}

// Fetch fetches the content identified by the descriptor.
func (s *Store) Fetch(ctx context.Context, target ocispec.Descriptor) (io.ReadCloser, error) {
	return s.storage.Fetch(ctx, target)
}

// Push pushes the content, matching the expected descriptor.
func (s *Store) Push(ctx context.Context, expected ocispec.Descriptor, content io.Reader) error {
	if err := s.storage.Push(ctx, expected, content); err != nil {
		return err
	}

	return s.graph.Index(ctx, s.storage, expected)
}

// Exists returns true if the described content exists.
func (s *Store) Exists(ctx context.Context, target ocispec.Descriptor) (bool, error) {
	return s.storage.Exists(ctx, target)
}

// Resolve resolves a reference to a descriptor.
func (s *Store) Resolve(ctx context.Context, ref string) (ocispec.Descriptor, error) {
	if ref == "" {
		return ocispec.Descriptor{}, errdef.ErrMissingReference
	}

	return s.resolver.Resolve(ctx, ref)
}

// Tag tags a descriptor with a reference string.
func (s *Store) Tag(ctx context.Context, desc ocispec.Descriptor, ref string) error {
	if ref == "" {
		return errdef.ErrMissingReference
	}

	exists, err := s.storage.Exists(ctx, desc)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("%s: %s: %w", desc.Digest, desc.MediaType, errdef.ErrNotFound)
	}

	return s.resolver.Tag(ctx, desc, ref)
}

// UpEdges returns the nodes directly pointing to the current node.
// UpEdges returns nil without error if the node does not exists in the store.
func (s *Store) UpEdges(ctx context.Context, node ocispec.Descriptor) ([]ocispec.Descriptor, error) {
	return s.graph.UpEdges(ctx, node)
}
