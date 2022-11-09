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
// Package graph traverses graphs.
package graph

import (
	"context"
	"errors"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
	"oras.land/oras-go/v2/internal/syncutil"
)

// Dispatch traverses a graph concurrently. To maximize the concurrency, the
// resulted search is neither depth-first nor breadth-first. For a rooted DAG,
// the root node is always traversed first and then its successors.
// On visiting a node,
// - `preHandler` is called before traversing the successors.
// - `postHandler` is called after traversing the successors.
// An optional concurrency limiter can be passed in to control the concurrency
// level.
// A handler may return `ErrSkipDesc` to signal not traversing descendants.
// If any handler returns an error, the entire dispatch is cancelled.
// This function is based on github.com/containerd/containerd/images.Dispatch.
// Note: Handlers with `github.com/containerd/containerd/images.ErrSkipDesc`
// cannot be used in this function.
// WARNING:
//   - This function does not detect circles. It is possible running into an
//     infinite loop. The caller is required to make sure the graph is a DAG.
//   - This function does not record walk history. Nodes might be visited multiple
//     times if they are directly pointed by multiple nodes.
func Dispatch(ctx context.Context, preHandler, postHandler Handler, limiter *semaphore.Weighted, roots ...ocispec.Descriptor) error {
	eg, egCtx := errgroup.WithContext(ctx)
	for _, root := range roots {
		lr := syncutil.NewLimitRegion(ctx, limiter)
		if err := lr.Begin(); err != nil {
			return err
		}
		eg.Go(func(desc ocispec.Descriptor) func() error {
			return func() (err error) {
				defer lr.End()

				// pre-handle
				nodes, err := preHandler.Handle(egCtx, desc)
				if err != nil {
					if errors.Is(err, ErrSkipDesc) {
						return nil
					}
					return err
				}

				// post-handle
				defer func() {
					if err == nil {
						_, err = postHandler.Handle(egCtx, desc)
						if err != nil && errors.Is(err, ErrSkipDesc) {
							err = nil
						}
					}
				}()

				// handle successors
				if len(nodes) > 0 {
					lr.End()

					err = Dispatch(egCtx, preHandler, postHandler, limiter, nodes...)
					if err != nil {
						return err
					}

					if err = lr.Begin(); err != nil {
						return err
					}
				}
				return nil
			}
		}(root))
	}
	return eg.Wait()
}
