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
	"oras.land/oras-go/v2/internal/copyutil"
	"oras.land/oras-go/v2/internal/descriptor"
)

// ExtendedCopyOptions contains parameters for oras.ExtendedCopy.
type ExtendedCopyOptions struct {
	ExtendedCopyGraphOptions
}

// ExtendedCopyGraphOptions contains parameters for oras.ExtendedCopyGraph.
type ExtendedCopyGraphOptions struct {
	CopyGraphOptions
	// Depth limits the maximum depth of the directed acyclic graph (DAG) that
	// will be extended-copied.
	// If Depth is no specified, or the specified value is less or equal than 0,
	// the depth limit will be considered as infinity.
	Depth int
	// UpEdgesFilter filters the up edges of the current node that is to be
	// extended-copied. Returns the filtered up edges.
	UpEdgesFilter func(ctx context.Context, node ocispec.Descriptor, upEdges []ocispec.Descriptor) ([]ocispec.Descriptor, error)
	// UpEdgesFinder finds the up edges of the current node.
	// If UpEdgesFinder is not provided, a default finder function will be used.
	UpEdgesFinder func(ctx context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error)
}

// ExtendedCopy copies the directed acyclic graph (DAG) that are reachable from
// the given tagged node from the source GraphTarget to the destination Target.
// The destination reference will be the same as the source reference if the
// destination reference is left blank.
// Returns the descriptor of the tagged node on successful copy.
func ExtendedCopy(ctx context.Context, src GraphTarget, srcRef string, dst Target, dstRef string, opts ExtendedCopyOptions) (ocispec.Descriptor, error) {
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

	if err := ExtendedCopyGraph(ctx, src, dst, node, opts.ExtendedCopyGraphOptions); err != nil {
		return ocispec.Descriptor{}, err
	}

	if err := dst.Tag(ctx, node, dstRef); err != nil {
		return ocispec.Descriptor{}, err
	}

	return node, nil
}

// ExtendedCopyGraph copies the directed acyclic graph (DAG) that are reachable
// from the given node from the source GraphStorage to the destination Storage.
func ExtendedCopyGraph(ctx context.Context, src content.GraphStorage, dst content.Storage, node ocispec.Descriptor, opts ExtendedCopyGraphOptions) error {
	roots, err := findRoots(ctx, src, node, opts)
	if err != nil {
		return err
	}

	// copy the sub-DAGs rooted by the root nodes
	for _, root := range roots {
		if err := CopyGraph(ctx, src, dst, root, opts.CopyGraphOptions); err != nil {
			return err
		}
	}

	return nil
}

// findRoots finds the root nodes reachable from the given node through a
// depth-first search.
func findRoots(ctx context.Context, finder content.UpEdgeFinder, node ocispec.Descriptor, opts ExtendedCopyGraphOptions) (map[descriptor.Descriptor]ocispec.Descriptor, error) {
	visited := make(map[descriptor.Descriptor]bool)
	roots := make(map[descriptor.Descriptor]ocispec.Descriptor)
	addRoot := func(key descriptor.Descriptor, val ocispec.Descriptor) {
		if _, exists := roots[key]; !exists {
			roots[key] = val
		}
	}

	stack := &copyutil.Stack{}
	// push the initial node to the stack, set the depth to 0
	stack.Push(copyutil.Item{Node: node, Depth: 0})
	for !stack.IsEmpty() {
		current, err := stack.Pop()
		if err != nil {
			return nil, err
		}
		currentNode := current.Node
		currentKey := descriptor.FromOCI(currentNode)

		if visited[currentKey] {
			// skip the current node if it has been visited
			continue
		}
		visited[currentKey] = true

		// stop finding parents if the target depth is reached
		if opts.Depth > 0 && current.Depth == opts.Depth {
			addRoot(currentKey, currentNode)
			continue
		}

		upEdges, err := getUpEdges(ctx, finder, currentNode, opts)
		if err != nil {
			return nil, err
		}

		// The current node has no parent node,
		// which means it is a root node of a sub-DAG.
		if len(upEdges) == 0 {
			addRoot(currentKey, currentNode)
			continue
		}

		// The current node has parent nodes, which means it is NOT a root node.
		// Push the parent nodes to the stack and keep finding from there.
		for _, upEdge := range upEdges {
			upEdgeKey := descriptor.FromOCI(upEdge)
			if !visited[upEdgeKey] {
				// push the parent node with increased depth
				stack.Push(copyutil.Item{Node: upEdge, Depth: current.Depth + 1})
			}
		}
	}
	return roots, nil
}

// getUpEdges returns the filtered up edges.
func getUpEdges(ctx context.Context, finder content.UpEdgeFinder, node ocispec.Descriptor, opts ExtendedCopyGraphOptions) ([]ocispec.Descriptor, error) {
	upEdgesFinder := opts.UpEdgesFinder
	if upEdgesFinder == nil {
		upEdgesFinder = finder.UpEdges
	}

	upEdges, err := upEdgesFinder(ctx, node)
	if err != nil {
		return nil, err
	}

	if opts.UpEdgesFilter != nil {
		return opts.UpEdgesFilter(ctx, node, upEdges)
	}

	return upEdges, nil
}
