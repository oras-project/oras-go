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
// Reference: https://github.com/opencontainers/image-spec/blob/v1.1.0-rc4/image-layout.md
package oci

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/internal/container/set"
	"oras.land/oras-go/v2/internal/descriptor"
	"oras.land/oras-go/v2/internal/graph"
	"oras.land/oras-go/v2/internal/resolver"
)

// DeletableStore implements `oras.Target`, and represents a content store
// extended with the delete operation.
// Reference: https://github.com/opencontainers/image-spec/blob/v1.1.0-rc4/image-layout.md
type DeletableStore struct {
	// AutoSaveIndex controls if the OCI store will automatically save the index
	// file on each Tag() call.
	//   - If AutoSaveIndex is set to true, the OCI store will automatically call
	//     this method on each Tag() call.
	//   - If AutoSaveIndex is set to false, it's the caller's responsibility
	//     to manually call SaveIndex() when needed.
	//   - Default value: true.
	AutoSaveIndex bool
	root          string
	indexPath     string
	index         *ocispec.Index
	indexLock     sync.Mutex
	operationLock sync.RWMutex

	storage     *Storage
	tagResolver *resolver.Memory
	graph       *graph.Memory
}

// NewDeletableStore returns a new DeletableStore.
func NewDeletableStore(root string) (*DeletableStore, error) {
	return NewDeletableStoreWithContext(context.Background(), root)
}

// NewDeletableStoreWithContext creates a new DeletableStore.
func NewDeletableStoreWithContext(ctx context.Context, root string) (*DeletableStore, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve absolute path for %s: %w", root, err)
	}
	storage, err := NewStorage(rootAbs)
	if err != nil {
		return nil, fmt.Errorf("failed to create storage: %w", err)
	}

	store := &DeletableStore{
		AutoSaveIndex: true,
		root:          rootAbs,
		indexPath:     filepath.Join(rootAbs, ociImageIndexFile),
		storage:       storage,
		tagResolver:   resolver.NewMemory(),
		graph:         graph.NewMemory(),
	}

	if err := ensureDir(filepath.Join(rootAbs, ociBlobsDir)); err != nil {
		return nil, err
	}
	if err := store.ensureOCILayoutFile(); err != nil {
		return nil, fmt.Errorf("invalid OCI Image Layout: %w", err)
	}
	if err := store.loadIndexFile(ctx); err != nil {
		return nil, fmt.Errorf("invalid OCI Image Index: %w", err)
	}

	return store, nil
}

// Fetch fetches the content identified by the descriptor.
func (s *DeletableStore) Fetch(ctx context.Context, target ocispec.Descriptor) (io.ReadCloser, error) {
	s.operationLock.RLock()
	defer s.operationLock.RUnlock()
	return s.storage.Fetch(ctx, target)
}

// Push pushes the content, matching the expected descriptor.
func (s *DeletableStore) Push(ctx context.Context, expected ocispec.Descriptor, reader io.Reader) error {
	s.operationLock.Lock()
	defer s.operationLock.Unlock()
	if err := s.storage.Push(ctx, expected, reader); err != nil {
		return err
	}
	if err := s.graph.Index(ctx, s.storage, expected); err != nil {
		return err
	}
	if descriptor.IsManifest(expected) {
		// tag by digest
		return s.tag(ctx, expected, expected.Digest.String())
	}
	return nil
}

// Delete removes the content matching the descriptor from the store.
func (s *DeletableStore) Delete(ctx context.Context, target ocispec.Descriptor) error {
	s.operationLock.Lock()
	defer s.operationLock.Unlock()
	resolvers := s.tagResolver.Map()
	for reference, desc := range resolvers {
		if content.Equal(desc, target) {
			s.tagResolver.Delete(reference)
		}
	}
	if err := s.graph.RemoveFromIndex(ctx, target); err != nil {
		return err
	}
	if s.AutoSaveIndex {
		err := s.SaveIndex()
		if err != nil {
			return err
		}
	}
	return s.storage.Delete(ctx, target)
}

// Exists returns true if the described content exists.
func (s *DeletableStore) Exists(ctx context.Context, target ocispec.Descriptor) (bool, error) {
	s.operationLock.RLock()
	defer s.operationLock.RUnlock()
	return s.storage.Exists(ctx, target)
}

// tag tags a descriptor with a reference string.
func (s *DeletableStore) tag(ctx context.Context, desc ocispec.Descriptor, reference string) error {
	dgst := desc.Digest.String()
	if reference != dgst {
		// also tag desc by its digest
		if err := s.tagResolver.Tag(ctx, desc, dgst); err != nil {
			return err
		}
	}
	if err := s.tagResolver.Tag(ctx, desc, reference); err != nil {
		return err
	}
	if s.AutoSaveIndex {
		return s.SaveIndex()
	}
	return nil
}

// Predecessors returns the nodes directly pointing to the current node.
// Predecessors returns nil without error if the node does not exists in the
// store.
func (s *DeletableStore) Predecessors(ctx context.Context, node ocispec.Descriptor) ([]ocispec.Descriptor, error) {
	return s.graph.Predecessors(ctx, node)
}

// ensureOCILayoutFile ensures the `oci-layout` file.
func (s *DeletableStore) ensureOCILayoutFile() error {
	layoutFilePath := filepath.Join(s.root, ocispec.ImageLayoutFile)
	layoutFile, err := os.Open(layoutFilePath)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("failed to open OCI layout file: %w", err)
		}

		layout := ocispec.ImageLayout{
			Version: ocispec.ImageLayoutVersion,
		}
		layoutJSON, err := json.Marshal(layout)
		if err != nil {
			return fmt.Errorf("failed to marshal OCI layout file: %w", err)
		}
		return os.WriteFile(layoutFilePath, layoutJSON, 0666)
	}
	defer layoutFile.Close()

	var layout ocispec.ImageLayout
	err = json.NewDecoder(layoutFile).Decode(&layout)
	if err != nil {
		return fmt.Errorf("failed to decode OCI layout file: %w", err)
	}
	return validateOCILayout(&layout)
}

// loadIndexFile reads index.json from the file system.
// Create index.json if it does not exist.
func (s *DeletableStore) loadIndexFile(ctx context.Context) error {
	indexFile, err := os.Open(s.indexPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("failed to open index file: %w", err)
		}

		// write index.json if it does not exist
		s.index = &ocispec.Index{
			Versioned: specs.Versioned{
				SchemaVersion: 2, // historical value
			},
			Manifests: []ocispec.Descriptor{},
		}
		return s.writeIndexFile()
	}
	defer indexFile.Close()

	var index ocispec.Index
	if err := json.NewDecoder(indexFile).Decode(&index); err != nil {
		return fmt.Errorf("failed to decode index file: %w", err)
	}
	s.index = &index
	return loadIndex(ctx, s.index, s.storage, s.tagResolver, s.graph)
}

// SaveIndex writes the `index.json` file to the file system.
//   - If AutoSaveIndex is set to true (default value),
//     the OCI store will automatically call this method on each Tag() call.
//   - If AutoSaveIndex is set to false, it's the caller's responsibility
//     to manually call this method when needed.
func (s *DeletableStore) SaveIndex() error {
	s.indexLock.Lock()
	defer s.indexLock.Unlock()

	var manifests []ocispec.Descriptor
	tagged := set.New[digest.Digest]()
	refMap := s.tagResolver.Map()

	// 1. Add descriptors that are associated with tags
	// Note: One descriptor can be associated with multiple tags.
	for ref, desc := range refMap {
		if ref != desc.Digest.String() {
			annotations := make(map[string]string, len(desc.Annotations)+1)
			for k, v := range desc.Annotations {
				annotations[k] = v
			}
			annotations[ocispec.AnnotationRefName] = ref
			desc.Annotations = annotations
			manifests = append(manifests, desc)
			// mark the digest as tagged for deduplication in step 2
			tagged.Add(desc.Digest)
		}
	}
	// 2. Add descriptors that are not associated with any tag
	for ref, desc := range refMap {
		if ref == desc.Digest.String() && !tagged.Contains(desc.Digest) {
			// skip tagged ones since they have been added in step 1
			manifests = append(manifests, deleteAnnotationRefName(desc))
		}
	}

	s.index.Manifests = manifests
	return s.writeIndexFile()
}

// writeIndexFile writes the `index.json` file.
func (s *DeletableStore) writeIndexFile() error {
	indexJSON, err := json.Marshal(s.index)
	if err != nil {
		return fmt.Errorf("failed to marshal index file: %w", err)
	}
	return os.WriteFile(s.indexPath, indexJSON, 0666)
}
