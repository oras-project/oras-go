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

package syncutil

import (
	"context"
	"sync"

	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

// LimitedRegion provides a way to bound concurrent access to a code block.
type LimitedRegion struct {
	ctx     context.Context
	limiter *semaphore.Weighted
	ended   bool
}

// LimitRegion creates a new LimitedRegion.
func LimitRegion(ctx context.Context, limiter *semaphore.Weighted) *LimitedRegion {
	if limiter == nil {
		return nil
	}
	return &LimitedRegion{
		ctx:     ctx,
		limiter: limiter,
		ended:   true,
	}
}

// Start starts the region with concurrency limit.
func (lr *LimitedRegion) Start() error {
	if lr == nil || !lr.ended {
		return nil
	}
	if err := lr.limiter.Acquire(lr.ctx, 1); err != nil {
		return err
	}
	lr.ended = false
	return nil
}

// End ends the region with concurrency limit.
func (lr *LimitedRegion) End() {
	if lr == nil || lr.ended {
		return
	}
	lr.limiter.Release(1)
	lr.ended = true
}

// GoFunc represents a function that can be invoked by Go.
type GoFunc[T any] func(ctx context.Context, region *LimitedRegion, t T) error

// Go concurrently invokes fn on items.
// It records the first “real” error (via sync.Once) and cancels the context.
// Tasks that see cancellation before running f() return nil, so that Wait()
// eventually returns your recorded error (if any).
func Go[T any](ctx context.Context, limiter *semaphore.Weighted, fn GoFunc[T], items ...T) error {
	// Create an explicit cancelable context.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	eg, egCtx := errgroup.WithContext(ctx)
	var once sync.Once
	var firstErr error

	for _, item := range items {
		region := LimitRegion(egCtx, limiter)
		if err := region.Start(); err != nil {
			once.Do(func() {
				firstErr = err
			})
			// Cancel other work and skip scheduling this task.
			cancel()
			// Instead of returning immediately, continue so that all previously
			// scheduled goroutines can run their deferred reg.End() calls.
			continue
		}

		// Capture item and region so the closure gets its own copy.
		eg.Go(func(t T, reg *LimitedRegion) func() error {
			return func() error {
				// Always ensure the acquired permit is released.
				defer reg.End()

				// If the context is already canceled (by a previous error),
				// skip executing fn() to avoid returning context.Canceled.
				select {
				case <-egCtx.Done():
					return nil
				default:
				}

				// Call the provided function.
				if err := fn(egCtx, reg, t); err != nil {
					once.Do(func() {
						firstErr = err
					})
					// Cancel other goroutines.
					cancel()
					return err
				}
				return nil
			}
		}(item, region))
	}

	if err := eg.Wait(); err != nil {
		if firstErr != nil {
			return firstErr
		}
		return err
	}
	return nil
}
