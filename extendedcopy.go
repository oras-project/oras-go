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

package oras

import (
	"context"
	"errors"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/internal/descriptor"
)

// ExtendedCopy copies the directed acyclic graph (DAG) that are reachable from
// the given tagged node from the source GraphTarget to the destination Target.
// The destination reference will be the same as the source reference if the
// destination reference is left blank.
// Returns the descriptor of the tagged node on successful copy.
func ExtendedCopy(ctx context.Context, src GraphTarget, srcRef string, dst Target, dstRef string) (ocispec.Descriptor, error) {
	if src == nil {
		return ocispec.Descriptor{}, errors.New("nil source traceable target")
	}
	if dst == nil {
		return ocispec.Descriptor{}, errors.New("nil destination target")
	}
	if dstRef == "" {
		dstRef = srcRef
	}

	node, err := src.Resolve(ctx, srcRef)
	if err != nil {
		return ocispec.Descriptor{}, err
	}

	if err := ExtendedCopyGraph(ctx, src, dst, node); err != nil {
		return ocispec.Descriptor{}, err
	}

	if err := dst.Tag(ctx, node, dstRef); err != nil {
		return ocispec.Descriptor{}, err
	}

	return node, nil
}

// ExtendedCopyGraph copies the directed acyclic graph (DAG) that are reachable
// from the given node from the source GraphStorage to the destination Storage.
func ExtendedCopyGraph(ctx context.Context, src content.GraphStorage, dst content.Storage, node ocispec.Descriptor) error {
	exists, err := src.Exists(ctx, node)
	if err != nil {
		return err
	}

	if !exists {
		return errdef.ErrNotFound
	}

	// find the root nodes traceable from the current node
	roots := make(map[descriptor.Descriptor]ocispec.Descriptor)
	if err := traceRoots(ctx, src, roots, node); err != nil {
		return err
	}

	// copy the sub-DAGs rooted by the root nodes
	for _, root := range roots {
		if err := CopyGraph(ctx, src, dst, root); err != nil {
			return err
		}
	}

	return nil
}

// traceRoots traces the root nodes from the given node,
// and records them in the given map.
func traceRoots(ctx context.Context, storage content.GraphStorage, roots map[descriptor.Descriptor]ocispec.Descriptor, node ocispec.Descriptor) error {
	exists, err := storage.Exists(ctx, node)
	if err != nil {
		return err
	}

	if !exists {
		return errdef.ErrNotFound
	}

	upEdges, err := storage.UpEdges(ctx, node)
	if err != nil {
		return err
	}

	// The current node has no parent node,
	// which means it is a root node of a sub-DAG.
	if len(upEdges) == 0 {
		key := descriptor.FromOCI(node)
		if _, exists := roots[key]; !exists {
			roots[key] = node
		}
		return nil
	}

	// The current node has parents nodes. Keep tracing from the parent nodes.
	for _, upEdge := range upEdges {
		if err := traceRoots(ctx, storage, roots, upEdge); err != nil {
			return err
		}
	}

	return nil
}
