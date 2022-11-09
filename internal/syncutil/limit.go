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

	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

type LimitRegion struct {
	ctx     context.Context
	limiter *semaphore.Weighted
	ended   bool
}

func NewLimitRegion(ctx context.Context, limiter *semaphore.Weighted) *LimitRegion {
	if limiter == nil {
		return nil
	}
	return &LimitRegion{
		ctx:     ctx,
		limiter: limiter,
		ended:   true,
	}
}

func (lr *LimitRegion) Begin() error {
	if lr == nil || !lr.ended {
		return nil
	}
	if err := lr.limiter.Acquire(lr.ctx, 1); err != nil {
		return err
	}
	lr.ended = false
	return nil
}

func (lr *LimitRegion) End() {
	if lr == nil || lr.ended {
		return
	}
	lr.limiter.Release(1)
	lr.ended = true
}

type GoFunc[T any] func(ctx context.Context, region *LimitRegion, t T) error

func Go[T any](ctx context.Context, limiter *semaphore.Weighted, fn GoFunc[T], items ...T) error {
	eg, egCtx := errgroup.WithContext(ctx)
	for _, item := range items {
		region := NewLimitRegion(ctx, limiter)
		if err := region.Begin(); err != nil {
			return err
		}
		eg.Go(func(t T) func() error {
			return func() error {
				defer region.End()
				return fn(egCtx, region, t)
			}
		}(item))
	}
	return eg.Wait()
}
