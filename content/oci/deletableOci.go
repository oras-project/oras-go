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
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/errdef"
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
func (ds *DeletableStore) Fetch(ctx context.Context, target ocispec.Descriptor) (io.ReadCloser, error) {
	ds.operationLock.RLock()
	defer ds.operationLock.RUnlock()
	return ds.storage.Fetch(ctx, target)
}

// Push pushes the content, matching the expected descriptor.
func (ds *DeletableStore) Push(ctx context.Context, expected ocispec.Descriptor, reader io.Reader) error {
	ds.operationLock.Lock()
	defer ds.operationLock.Unlock()
	if err := ds.storage.Push(ctx, expected, reader); err != nil {
		return err
	}
	if err := ds.graph.Index(ctx, ds.storage, expected); err != nil {
		return err
	}
	if descriptor.IsManifest(expected) {
		// tag by digest
		return ds.tag(ctx, expected, expected.Digest.String())
	}
	return nil
}

// Delete removes the content matching the descriptor from the store.
func (ds *DeletableStore) Delete(ctx context.Context, target ocispec.Descriptor) error {
	ds.operationLock.Lock()
	defer ds.operationLock.Unlock()
	resolvers := ds.tagResolver.Map()
	for reference, desc := range resolvers {
		if content.Equal(desc, target) {
			ds.tagResolver.Delete(reference)
		}
	}
	if err := ds.graph.RemoveFromIndex(ctx, target); err != nil {
		return err
	}
	if ds.AutoSaveIndex {
		err := ds.SaveIndex()
		if err != nil {
			return err
		}
	}
	return ds.storage.Delete(ctx, target)
}

// Exists returns true if the described content exists.
func (ds *DeletableStore) Exists(ctx context.Context, target ocispec.Descriptor) (bool, error) {
	ds.operationLock.RLock()
	defer ds.operationLock.RUnlock()
	return ds.storage.Exists(ctx, target)
}

// tag tags a descriptor with a reference string.
func (ds *DeletableStore) tag(ctx context.Context, desc ocispec.Descriptor, reference string) error {
	dgst := desc.Digest.String()
	if reference != dgst {
		// also tag desc by its digest
		if err := ds.tagResolver.Tag(ctx, desc, dgst); err != nil {
			return err
		}
	}
	if err := ds.tagResolver.Tag(ctx, desc, reference); err != nil {
		return err
	}
	if ds.AutoSaveIndex {
		return ds.SaveIndex()
	}
	return nil
}

// Resolve resolves a reference to a descriptor. If the reference to be resolved
// is a tag, the returned descriptor will be a full descriptor declared by
// github.com/opencontainers/image-spec/specs-go/v1. If the reference is a
// digest the returned descriptor will be a plain descriptor (containing only
// the digest, media type and size).
func (ds *DeletableStore) Resolve(ctx context.Context, reference string) (ocispec.Descriptor, error) {
	ds.operationLock.RLock()
	defer ds.operationLock.RUnlock()
	if reference == "" {
		return ocispec.Descriptor{}, errdef.ErrMissingReference
	}

	// attempt resolving manifest
	desc, err := ds.tagResolver.Resolve(ctx, reference)
	if err != nil {
		if errors.Is(err, errdef.ErrNotFound) {
			// attempt resolving blob
			return resolveBlob(os.DirFS(ds.root), reference)
		}
		return ocispec.Descriptor{}, err
	}

	if reference == desc.Digest.String() {
		return descriptor.Plain(desc), nil
	}

	return desc, nil
}

// Predecessors returns the nodes directly pointing to the current node.
// Predecessors returns nil without error if the node does not exists in the
// store.
func (ds *DeletableStore) Predecessors(ctx context.Context, node ocispec.Descriptor) ([]ocispec.Descriptor, error) {
	return ds.graph.Predecessors(ctx, node)
}

// ensureOCILayoutFile ensures the `oci-layout` file.
func (ds *DeletableStore) ensureOCILayoutFile() error {
	layoutFilePath := filepath.Join(ds.root, ocispec.ImageLayoutFile)
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
func (ds *DeletableStore) loadIndexFile(ctx context.Context) error {
	indexFile, err := os.Open(ds.indexPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("failed to open index file: %w", err)
		}

		// write index.json if it does not exist
		ds.index = &ocispec.Index{
			Versioned: specs.Versioned{
				SchemaVersion: 2, // historical value
			},
			Manifests: []ocispec.Descriptor{},
		}
		return ds.writeIndexFile()
	}
	defer indexFile.Close()

	var index ocispec.Index
	if err := json.NewDecoder(indexFile).Decode(&index); err != nil {
		return fmt.Errorf("failed to decode index file: %w", err)
	}
	ds.index = &index
	return loadIndex(ctx, ds.index, ds.storage, ds.tagResolver, ds.graph)
}

// SaveIndex writes the `index.json` file to the file system.
//   - If AutoSaveIndex is set to true (default value),
//     the OCI store will automatically call this method on each Tag() call.
//   - If AutoSaveIndex is set to false, it's the caller's responsibility
//     to manually call this method when needed.
func (ds *DeletableStore) SaveIndex() error {
	ds.indexLock.Lock()
	defer ds.indexLock.Unlock()

	var manifests []ocispec.Descriptor
	tagged := set.New[digest.Digest]()
	refMap := ds.tagResolver.Map()

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

	ds.index.Manifests = manifests
	return ds.writeIndexFile()
}

// writeIndexFile writes the `index.json` file.
func (ds *DeletableStore) writeIndexFile() error {
	indexJSON, err := json.Marshal(ds.index)
	if err != nil {
		return fmt.Errorf("failed to marshal index file: %w", err)
	}
	return os.WriteFile(ds.indexPath, indexJSON, 0666)
}
