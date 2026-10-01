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
	"github.com/oras-project/oras-go/v3/content"
	"github.com/oras-project/oras-go/v3/internal/cas"
	"github.com/oras-project/oras-go/v3/internal/status"
	"github.com/oras-project/oras-go/v3/internal/syncutil"
	"golang.org/x/sync/semaphore"
)

// DefaultExtendedVerifyOptions provides the default ExtendedVerifyOptions.
var DefaultExtendedVerifyOptions ExtendedVerifyOptions = ExtendedVerifyOptions{
	ExtendedVerifyGraphOptions: DefaultExtendedVerifyGraphOptions,
}

// ExtendedVerifyOptions contains parameters for [oras.ExtendedVerify].
type ExtendedVerifyOptions struct {
	ExtendedVerifyGraphOptions
}

// DefaultExtendedVerifyGraphOptions provides the default ExtendedVerifyGraphOptions.
var DefaultExtendedVerifyGraphOptions ExtendedVerifyGraphOptions = ExtendedVerifyGraphOptions{
	VerifyGraphOptions: DefaultVerifyGraphOptions,
}

// ExtendedVerifyGraphOptions contains parameters for [oras.ExtendedVerifyGraph].
type ExtendedVerifyGraphOptions struct {
	VerifyGraphOptions
	// Depth limits the maximum depth of the directed acyclic graph (DAG) that
	// will be extended-verified.
	// If Depth is not specified, or the specified value is less than or
	// equal to 0, the depth limit will be considered as infinity.
	Depth int
	// FindPredecessors finds the predecessors of the current node.
	// If FindPredecessors is nil, src.Predecessors will be adapted and used.
	FindPredecessors func(ctx context.Context, src content.ReadOnlyGraphStorage, desc ocispec.Descriptor) ([]ocispec.Descriptor, error)
}

// ExtendedVerify verifies the directed acyclic graph (DAG) that are reachable from
// the given tagged node from the source GraphTarget.
// In other words, it verifies a tagged artifact along with its referrers or
// other predecessor manifests referencing it.
//
// See [Verify] for the "on error resume next" behavior. If the walk stops
// after ref was resolved, the partial report is returned along with the error.
func ExtendedVerify(ctx context.Context, src ReadOnlyGraphTarget, ref string, opts ExtendedVerifyOptions) (ocispec.Descriptor, *VerifyReport, error) {

	if src == nil {
		return ocispec.Descriptor{}, nil, newCopyError("ExtendedVerify", CopyErrorOriginSource, ocispec.Descriptor{}, errors.New("nil source target"))
	}

	node, err := src.Resolve(ctx, ref)
	if err != nil {
		return ocispec.Descriptor{}, nil, newCopyError("Resolve", CopyErrorOriginSource, ocispec.Descriptor{}, err)
	}

	graphReport, err := ExtendedVerifyGraph(ctx, src, node, opts.ExtendedVerifyGraphOptions)
	if err != nil && graphReport == nil {
		return node, nil, err
	}

	return node, &VerifyReport{Node: node, VerifyGraphReport: graphReport}, err
}

// ExtendedVerifyGraph verifies the directed acyclic graph (DAG) that are reachable
// from the given node from the source GraphStorage.
// In other words, it verifies an artifact along with its referrers or other
// predecessor manifests referencing it.
// The node (e.g. a manifest of the artifact) is identified by a descriptor.
func ExtendedVerifyGraph(ctx context.Context, src content.ReadOnlyGraphStorage, node ocispec.Descriptor, opts ExtendedVerifyGraphOptions) (*VerifyGraphReport, error) {
	if src == nil {
		return nil, newCopyError("ExtendedVerifyGraph", CopyErrorOriginSource, ocispec.Descriptor{}, errors.New("nil source target"))
	}

	// if Concurrency is not set or invalid, use the default concurrency
	if opts.Concurrency <= 0 {
		opts.Concurrency = defaultConcurrency
	}
	limiter := semaphore.NewWeighted(int64(opts.Concurrency))

	roots, err := findRoots(ctx, src, node, findRootsOptions{
		Depth:            opts.Depth,
		FindPredecessors: opts.FindPredecessors,
	}, limiter)
	if err != nil {
		return nil, err
	}

	// use caching proxy on non-leaf nodes
	if opts.MaxMetadataBytes <= 0 {
		opts.MaxMetadataBytes = defaultCopyMaxMetadataBytes
	}
	proxy := cas.NewProxyWithLimit(src, cas.NewMemory(), opts.MaxMetadataBytes)

	// track content status
	tracker := status.NewTracker()

	// verify the sub-DAGs rooted by the root nodes
	report := newVerifyGraphReport()

	err = syncutil.Go(ctx, limiter, func(
		ctx context.Context,
		region *syncutil.LimitedRegion,
		root ocispec.Descriptor,
	) error {
		// As a root can be a predecessor of other roots, release the limit
		// here for dispatching, to avoid deadlocks where predecessor roots
		// are handled first and are waiting for their successors to complete.
		region.End()

		subReport, err := verifyGraph(
			ctx,
			src,
			root,
			proxy,
			limiter,
			tracker,
			opts.VerifyGraphOptions,
		)
		// merge before checking err: on a walk failure subReport still
		// holds the nodes processed so far.
		report.merge(subReport)

		if err != nil {
			return err
		}

		return region.Start()
	}, roots...)

	if err != nil {
		return report, err
	}

	return report, nil
}
