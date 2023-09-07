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

package graph

import (
	"context"
	"errors"
	"sync"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/internal/descriptor"
	"oras.land/oras-go/v2/internal/status"
	"oras.land/oras-go/v2/internal/syncutil"
)

// MemoryWithDelete is a MemoryWithDelete based PredecessorFinder.
type MemoryWithDelete struct {
	indexed      sync.Map // map[descriptor.Descriptor]any
	predecessors sync.Map // map[descriptor.Descriptor]map[descriptor.Descriptor]ocispec.Descriptor
	successors   sync.Map // map[descriptor.Descriptor]map[descriptor.Descriptor]ocispec.Descriptor
	lock         sync.Mutex
}

// NewMemoryWithDelete creates a new MemoryWithDelete PredecessorFinder.
func NewMemoryWithDelete() *MemoryWithDelete {
	return &MemoryWithDelete{}
}

// Index indexes predecessors for each direct successor of the given node.
// There is no data consistency issue as long as deletion is not implemented
// for the underlying storage.
func (m *MemoryWithDelete) Index(ctx context.Context, fetcher content.Fetcher, node ocispec.Descriptor) error {
	successors, err := content.Successors(ctx, fetcher, node)
	if err != nil {
		return err
	}

	m.index(ctx, node, successors)
	return nil
}

// Index indexes predecessors for all the successors of the given node.
// There is no data consistency issue as long as deletion is not implemented
// for the underlying storage.
func (m *MemoryWithDelete) IndexAll(ctx context.Context, fetcher content.Fetcher, node ocispec.Descriptor) error {
	// track content status
	tracker := status.NewTracker()

	var fn syncutil.GoFunc[ocispec.Descriptor]
	fn = func(ctx context.Context, region *syncutil.LimitedRegion, desc ocispec.Descriptor) error {
		// skip the node if other go routine is working on it
		_, committed := tracker.TryCommit(desc)
		if !committed {
			return nil
		}

		// skip the node if it has been indexed
		key := descriptor.FromOCI(desc)
		_, exists := m.indexed.Load(key)
		if exists {
			return nil
		}

		successors, err := content.Successors(ctx, fetcher, desc)
		if err != nil {
			if errors.Is(err, errdef.ErrNotFound) {
				// skip the node if it does not exist
				return nil
			}
			return err
		}
		m.index(ctx, desc, successors)
		m.indexed.Store(key, nil)

		if len(successors) > 0 {
			// traverse and index successors
			return syncutil.Go(ctx, nil, fn, successors...)
		}
		return nil
	}
	return syncutil.Go(ctx, nil, fn, node)
}

// Predecessors returns the nodes directly pointing to the current node.
// Predecessors returns nil without error if the node does not exists in the
// store.
// Like other operations, calling Predecessors() is go-routine safe. However,
// it does not necessarily correspond to any consistent snapshot of the stored
// contents.
func (m *MemoryWithDelete) Predecessors(_ context.Context, node ocispec.Descriptor) ([]ocispec.Descriptor, error) {
	key := descriptor.FromOCI(node)
	value, exists := m.predecessors.Load(key)
	if !exists {
		return nil, nil
	}
	predecessors := value.(*sync.Map)

	var res []ocispec.Descriptor
	predecessors.Range(func(key, value interface{}) bool {
		res = append(res, value.(ocispec.Descriptor))
		return true
	})
	return res, nil
}

// Remove removes the node from its predecessors and successors.
func (m *MemoryWithDelete) Remove(ctx context.Context, node ocispec.Descriptor) error {
	m.lock.Lock()
	defer m.lock.Unlock()
	nodeKey := descriptor.FromOCI(node)
	// remove the node from its successors' predecessor list
	value, _ := m.successors.Load(nodeKey)
	successors := value.(*sync.Map)
	successors.Range(func(key, _ interface{}) bool {
		value, _ = m.predecessors.Load(key)
		predecessors := value.(*sync.Map)
		predecessors.Delete(nodeKey)
		return true
	})
	m.removeEntriesFromMaps(ctx, node)
	return nil
}

// index indexes predecessors for each direct successor of the given node.
// There is no data consistency issue as long as deletion is not implemented
// for the underlying storage.
func (m *MemoryWithDelete) index(ctx context.Context, node ocispec.Descriptor, successors []ocispec.Descriptor) {
	m.lock.Lock()
	defer m.lock.Unlock()
	m.createEntriesInMaps(ctx, node)
	if len(successors) == 0 {
		return
	}
	predecessorKey := descriptor.FromOCI(node)
	for _, successor := range successors {
		successorKey := descriptor.FromOCI(successor)
		// store in m.predecessors, MemoryWithDelete.predecessors[successorKey].Store(node)
		pred, _ := m.predecessors.LoadOrStore(successorKey, &sync.Map{})
		predecessorsMap := pred.(*sync.Map)
		predecessorsMap.Store(predecessorKey, node)
		// store in m.successors, MemoryWithDelete.successors[predecessorKey].Store(successor)
		succ, _ := m.successors.Load(predecessorKey)
		successorsMap := succ.(*sync.Map)
		successorsMap.Store(successorKey, successor)
	}
}

func (m *MemoryWithDelete) createEntriesInMaps(ctx context.Context, node ocispec.Descriptor) {
	key := descriptor.FromOCI(node)
	m.predecessors.LoadOrStore(key, &sync.Map{})
	m.successors.LoadOrStore(key, &sync.Map{})
}

func (m *MemoryWithDelete) removeEntriesFromMaps(ctx context.Context, node ocispec.Descriptor) {
	key := descriptor.FromOCI(node)
	m.predecessors.Delete(key)
	m.successors.Delete(key)
	m.indexed.Delete(key)
}
