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
	"sync"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/internal/descriptor"
	"oras.land/oras-go/v2/internal/status"
)

// Memory is a memory based UpEdgeFinder.
type Memory struct {
	upEdges sync.Map // map[descriptor.Descriptor]map[descriptor.Descriptor]ocispec.Descriptor
	indexed sync.Map // map[descriptor.Descriptor]bool
}

// NewMemory creates a new memory UpEdgeFinder.
func NewMemory() *Memory {
	return &Memory{}
}

// Index indexes up edges for each direct down edge of the given node.
// There is no data consistency issue as long as deletion is not implemented
// for the underlying storage.
func (m *Memory) Index(ctx context.Context, fetcher content.Fetcher, node ocispec.Descriptor) error {
	downEdges, err := content.DownEdges(ctx, fetcher, node)
	if err != nil {
		return err
	}

	return m.index(ctx, node, downEdges)
}

// Index indexes up edges for all the down edges of the given node.
// There is no data consistency issue as long as deletion is not implemented
// for the underlying storage.
func (m *Memory) IndexAll(ctx context.Context, fetcher content.Fetcher, node ocispec.Descriptor) error {
	// track content status
	tracker := status.NewTracker()

	// prepare pre-handler
	preHandler := HandlerFunc(func(ctx context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
		// skip the node if other go routine is working on it
		_, committed := tracker.TryCommit(desc)
		if !committed {
			return nil, ErrSkipDesc
		}

		// skip the node if it has been indexed
		key := descriptor.FromOCI(desc)
		_, exists := m.indexed.Load(key)
		if exists {
			return nil, ErrSkipDesc
		}

		downEdges, err := content.DownEdges(ctx, fetcher, desc)
		if err != nil {
			return nil, err
		}

		if err := m.index(ctx, desc, downEdges); err != nil {
			return nil, err
		}

		return downEdges, nil
	})

	postHandler := Handlers()

	// traverse the graph
	return Dispatch(ctx, preHandler, postHandler, nil, node)
}

// UpEdges returns the nodes directly pointing to the current node.
// UpEdges returns nil without error if the node does not exists in the store.
// Like other operations, calling UpEdges() is go-routine safe. However, it does
// not necessarily correspond to any consistent snapshot of the stored contents.
func (m *Memory) UpEdges(_ context.Context, node ocispec.Descriptor) ([]ocispec.Descriptor, error) {
	key := descriptor.FromOCI(node)
	value, exists := m.upEdges.Load(key)
	if !exists {
		return nil, nil
	}
	upEdges := value.(*sync.Map)

	var res []ocispec.Descriptor
	upEdges.Range(func(key, value interface{}) bool {
		res = append(res, value.(ocispec.Descriptor))
		return true
	})
	return res, nil
}

// index indexes up edges for each direct down edge of the given node.
// There is no data consistency issue as long as deletion is not implemented
// for the underlying storage.
func (m *Memory) index(ctx context.Context, node ocispec.Descriptor, downEdges []ocispec.Descriptor) error {
	upEdgeKey := descriptor.FromOCI(node)

	for _, downEdge := range downEdges {
		downEdgeKey := descriptor.FromOCI(downEdge)
		value, _ := m.upEdges.LoadOrStore(downEdgeKey, &sync.Map{})
		upEdges := value.(*sync.Map)
		upEdges.Store(upEdgeKey, node)
	}

	m.indexed.Store(upEdgeKey, true)
	return nil
}
