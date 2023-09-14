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
	"oras.land/oras-go/v2/internal/container/set"
	"oras.land/oras-go/v2/internal/descriptor"
	"oras.land/oras-go/v2/internal/status"
	"oras.land/oras-go/v2/internal/syncutil"
)

// DeletableMemory is a memory based PredecessorFinder.
type DeletableMemory struct {
	nodes        map[descriptor.Descriptor]ocispec.Descriptor // nodes saves the map keys of ocispec.Descriptor
	predecessors map[descriptor.Descriptor]set.Set[descriptor.Descriptor]
	successors   map[descriptor.Descriptor]set.Set[descriptor.Descriptor]
	lock         sync.RWMutex
}

// NewDeletableMemory creates a new DeletableMemory.
func NewDeletableMemory() *DeletableMemory {
	return &DeletableMemory{
		nodes:        make(map[descriptor.Descriptor]ocispec.Descriptor),
		predecessors: make(map[descriptor.Descriptor]set.Set[descriptor.Descriptor]),
		successors:   make(map[descriptor.Descriptor]set.Set[descriptor.Descriptor]),
	}
}

// Index indexes predecessors for each direct successor of the given node.
func (m *DeletableMemory) Index(ctx context.Context, fetcher content.Fetcher, node ocispec.Descriptor) error {
	_, err := m.index(ctx, fetcher, node)
	return err
}

// Index indexes predecessors for all the successors of the given node.
func (m *DeletableMemory) IndexAll(ctx context.Context, fetcher content.Fetcher, node ocispec.Descriptor) error {
	// track content status
	tracker := status.NewTracker()

	var fn syncutil.GoFunc[ocispec.Descriptor]
	fn = func(ctx context.Context, region *syncutil.LimitedRegion, desc ocispec.Descriptor) error {
		// skip the node if other go routine is working on it
		_, committed := tracker.TryCommit(desc)
		if !committed {
			return nil
		}

		successors, err := m.index(ctx, fetcher, desc)
		if err != nil {
			if errors.Is(err, errdef.ErrNotFound) {
				// skip the node if it does not exist
				return nil
			}
			return err
		}

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
func (m *DeletableMemory) Predecessors(_ context.Context, node ocispec.Descriptor) ([]ocispec.Descriptor, error) {
	m.lock.RLock()
	defer m.lock.RUnlock()

	key := descriptor.FromOCI(node)
	set, exists := m.predecessors[key]
	if !exists {
		return nil, nil
	}
	var res []ocispec.Descriptor
	for k := range set {
		res = append(res, m.nodes[k])
	}
	return res, nil
}

// Remove removes the node from its predecessors and successors.
func (m *DeletableMemory) Remove(ctx context.Context, node ocispec.Descriptor) error {
	nodeKey := descriptor.FromOCI(node)
	m.lock.Lock()
	defer m.lock.Unlock()
	// remove the node from its successors' predecessor list
	for successorKey := range m.successors[nodeKey] {
		m.predecessors[successorKey].Delete(successorKey)
	}
	delete(m.successors, nodeKey)
	return nil
}

// index indexes predecessors for each direct successor of the given node.
func (m *DeletableMemory) index(ctx context.Context, fetcher content.Fetcher, node ocispec.Descriptor) ([]ocispec.Descriptor, error) {
	successors, err := content.Successors(ctx, fetcher, node)
	if err != nil {
		return nil, err
	}
	m.lock.Lock()
	defer m.lock.Unlock()

	// index the node
	nodeKey := descriptor.FromOCI(node)
	m.nodes[nodeKey] = node

	// index the successors and predecessors
	successorSet := set.New[descriptor.Descriptor]()
	m.successors[nodeKey] = successorSet

	for _, successor := range successors {
		successorKey := descriptor.FromOCI(successor)
		successorSet.Add(successorKey)
		predecessorSet, exists := m.predecessors[nodeKey]
		if !exists {
			predecessorSet = set.New[descriptor.Descriptor]()
			m.predecessors[successorKey] = predecessorSet
		}
		predecessorSet.Add(nodeKey)
	}
	return successors, nil
}
