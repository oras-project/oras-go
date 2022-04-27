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
	"oras.land/oras-go/v2/internal/descriptor"
)

// ExtendedCopy copies the directed acyclic graph (DAG) that are reachable from
// the given tagged node from the source GraphTarget to the destination Target.
// The destination reference will be the same as the source reference if the
// destination reference is left blank.
// Returns the descriptor of the tagged node on successful copy.
func ExtendedCopy(ctx context.Context, src GraphTarget, srcRef string, dst Target, dstRef string) (ocispec.Descriptor, error) {
	if src == nil {
		return ocispec.Descriptor{}, errors.New("nil source graph target")
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
	roots, err := findRoots(ctx, src, node)
	if err != nil {
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

// findRoots finds the root nodes reachable from the given node through a
// depth-first search.
func findRoots(ctx context.Context, finder content.UpEdgeFinder, node ocispec.Descriptor) (map[descriptor.Descriptor]ocispec.Descriptor, error) {
	roots := make(map[descriptor.Descriptor]ocispec.Descriptor)
	visited := make(map[descriptor.Descriptor]bool)
	var stack []ocispec.Descriptor

	// push the initial node to the stack
	stack = append(stack, node)
	for len(stack) > 0 {
		// pop the current node from the stack
		top := len(stack) - 1
		current := stack[top]
		stack = stack[:top]

		currentKey := descriptor.FromOCI(current)
		if visited[currentKey] {
			// skip the current node if it has been visited
			continue
		}
		visited[currentKey] = true

		upEdges, err := finder.UpEdges(ctx, current)
		if err != nil {
			return nil, err
		}

		// The current node has no parent node,
		// which means it is a root node of a sub-DAG.
		if len(upEdges) == 0 {
			if _, exists := roots[currentKey]; !exists {
				roots[currentKey] = current
			}
			continue
		}

		// The current node has parent nodes, which means it is NOT a root node.
		// Push the parent nodes to the stack and keep finding from there.
		for _, upEdge := range upEdges {
			upEdgeKey := descriptor.FromOCI(upEdge)
			if !visited[upEdgeKey] {
				stack = append(stack, upEdge)
			}
		}
	}

	return roots, nil
}
