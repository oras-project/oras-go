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
	"regexp"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/internal/copyutil"
	"oras.land/oras-go/v2/internal/descriptor"
)

var (
	// DefaultExtendedCopyOptions provides the default ExtendedCopyOptions.
	DefaultExtendedCopyOptions = ExtendedCopyOptions{
		ExtendedCopyGraphOptions: DefaultExtendedCopyGraphOptions,
	}
	// DefaultExtendedCopyGraphOptions provides the default ExtendedCopyGraphOptions.
	DefaultExtendedCopyGraphOptions = ExtendedCopyGraphOptions{
		CopyGraphOptions: DefaultCopyGraphOptions,
	}
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
	// FindPredecessors finds the predecessors of the current node.
	// If FindPredecessors is nil, src.Predecessors will be adapted and used.
	FindPredecessors func(ctx context.Context, src content.GraphStorage, desc ocispec.Descriptor) ([]ocispec.Descriptor, error)
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

func (opts *ExtendedCopyGraphOptions) FilterOnAnnotation(key string, regex string) {
	opts.FindPredecessors = func(ctx context.Context, src content.GraphStorage, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
		predecessors, err := src.Predecessors(ctx, desc)
		if err != nil {
			return nil, err
		}
		var filtered []ocispec.Descriptor
		for _, p := range predecessors {
			matched, err := regexp.MatchString(regex, p.Annotations[key])
			if err != nil {
				return nil, err
			}
			if matched {
				filtered = append(filtered, p)
			}
		}
		return filtered, nil
	}
}

// findRoots finds the root nodes reachable from the given node through a
// depth-first search.
func findRoots(ctx context.Context, storage content.GraphStorage, node ocispec.Descriptor, opts ExtendedCopyGraphOptions) (map[descriptor.Descriptor]ocispec.Descriptor, error) {
	visited := make(map[descriptor.Descriptor]bool)
	roots := make(map[descriptor.Descriptor]ocispec.Descriptor)
	addRoot := func(key descriptor.Descriptor, val ocispec.Descriptor) {
		if _, exists := roots[key]; !exists {
			roots[key] = val
		}
	}

	// if FindPredecessors is not provided, use the default one
	if opts.FindPredecessors == nil {
		opts.FindPredecessors = func(ctx context.Context, src content.GraphStorage, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
			return src.Predecessors(ctx, desc)
		}
	}

	var stack copyutil.Stack
	// push the initial node to the stack, set the depth to 0
	stack.Push(copyutil.NodeInfo{Node: node, Depth: 0})
	for {
		current, ok := stack.Pop()
		if !ok {
			// empty stack
			break
		}
		currentNode := current.Node
		currentKey := descriptor.FromOCI(currentNode)

		if visited[currentKey] {
			// skip the current node if it has been visited
			continue
		}
		visited[currentKey] = true

		// stop finding predecessors if the target depth is reached
		if opts.Depth > 0 && current.Depth == opts.Depth {
			addRoot(currentKey, currentNode)
			continue
		}

		predecessors, err := opts.FindPredecessors(ctx, storage, currentNode)
		if err != nil {
			return nil, err
		}

		// The current node has no predecessor node,
		// which means it is a root node of a sub-DAG.
		if len(predecessors) == 0 {
			addRoot(currentKey, currentNode)
			continue
		}

		// The current node has predecessor nodes, which means it is NOT a root node.
		// Push the predecessor nodes to the stack and keep finding from there.
		for _, predecessor := range predecessors {
			predecessorKey := descriptor.FromOCI(predecessor)
			if !visited[predecessorKey] {
				// push the predecessor node with increased depth
				stack.Push(copyutil.NodeInfo{Node: predecessor, Depth: current.Depth + 1})
			}
		}
	}
	return roots, nil
}
