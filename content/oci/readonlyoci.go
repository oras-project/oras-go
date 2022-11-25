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

// Package oci provides access to an OCI content store.
// Reference: https://github.com/opencontainers/image-spec/blob/main/image-layout.md
package oci

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/internal/graph"
	"oras.land/oras-go/v2/internal/resolver"
)

// ReadOnlyStore implements `oras.ReadonlyTarget`, and represents a read-only
// content store based on file system with the OCI-Image layout.
// Reference: https://github.com/opencontainers/image-spec/blob/master/image-layout.md
type ReadOnlyStore struct {
	fsys     fs.FS
	storage  content.ReadOnlyStorage
	resolver *resolver.Memory
	graph    *graph.Memory
}

// NewFromFS creates a new read-only OCI store from fsys with
// context.Background().
func NewFromFS(fsys fs.FS) (*ReadOnlyStore, error) {
	return NewFromFSWithContext(context.Background(), fsys)
}

// NewFromFSWithContext creates a new read-only OCI store from fsys.
func NewFromFSWithContext(ctx context.Context, fsys fs.FS) (*ReadOnlyStore, error) {
	store := &ReadOnlyStore{
		fsys:     fsys,
		storage:  NewStorageFromFS(fsys),
		resolver: resolver.NewMemory(),
		graph:    graph.NewMemory(),
	}

	if err := store.validateOCILayoutFile(); err != nil {
		return nil, err
	}
	if err := store.loadIndex(ctx); err != nil {
		return nil, err
	}

	return store, nil
}

// Fetch fetches the content identified by the descriptor.
func (s *ReadOnlyStore) Fetch(ctx context.Context, target ocispec.Descriptor) (io.ReadCloser, error) {
	return s.storage.Fetch(ctx, target)
}

// Exists returns true if the described content exists.
func (s *ReadOnlyStore) Exists(ctx context.Context, target ocispec.Descriptor) (bool, error) {
	return s.storage.Exists(ctx, target)
}

// Resolve resolves a reference to a descriptor.
func (s *ReadOnlyStore) Resolve(ctx context.Context, reference string) (ocispec.Descriptor, error) {
	if reference == "" {
		return ocispec.Descriptor{}, errdef.ErrMissingReference
	}

	return s.resolver.Resolve(ctx, reference)
}

// Predecessors returns the nodes directly pointing to the current node.
// Predecessors returns nil without error if the node does not exists in the
// store.
func (s *ReadOnlyStore) Predecessors(ctx context.Context, node ocispec.Descriptor) ([]ocispec.Descriptor, error) {
	return s.graph.Predecessors(ctx, node)
}

// validateOCILayoutFile validates the `oci-layout` file.
func (s *ReadOnlyStore) validateOCILayoutFile() error {
	layoutFile, err := s.fsys.Open(ocispec.ImageLayoutFile)
	if err != nil {
		return fmt.Errorf("failed to open OCI layout file: %w", err)
	}
	defer layoutFile.Close()

	var layout *ocispec.ImageLayout
	err = json.NewDecoder(layoutFile).Decode(&layout)
	if err != nil {
		return fmt.Errorf("failed to decode OCI layout file: %w", err)
	}
	if layout.Version != ocispec.ImageLayoutVersion {
		return errdef.ErrUnsupportedVersion
	}
	return nil
}

// loadIndex reads the index.json from s.fsys.
func (s *ReadOnlyStore) loadIndex(ctx context.Context) error {
	indexFile, err := s.fsys.Open(ociImageIndexFile)
	if err != nil {
		return fmt.Errorf("failed to open index file: %w", err)
	}
	defer indexFile.Close()

	var index ocispec.Index
	if err := json.NewDecoder(indexFile).Decode(&index); err != nil {
		return fmt.Errorf("failed to decode index file: %w", err)
	}
	for _, desc := range index.Manifests {
		if ref := desc.Annotations[ocispec.AnnotationRefName]; ref != "" {
			if err = s.resolver.Tag(ctx, desc, ref); err != nil {
				return err
			}
		}

		// traverse the whole DAG and index predecessors for all the nodes.
		if err := s.graph.IndexAll(ctx, s.storage, desc); err != nil {
			return err
		}
	}

	return nil
}
