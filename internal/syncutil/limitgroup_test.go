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
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestLimitedGroup_Success(t *testing.T) {
	ctx := context.Background()
	numTasks := 5

	// Create a limited group with a concurrency limit of 2.
	lg, _ := LimitGroup(ctx, 2)
	var counter int32

	for range numTasks {
		lg.Go(func() error {
			// simulate some work.
			time.Sleep(10 * time.Millisecond)
			atomic.AddInt32(&counter, 1)
			return nil
		})
	}

	if err := lg.Wait(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := atomic.LoadInt32(&counter); got != int32(numTasks) {
		t.Errorf("expected counter %d, got %d", numTasks, got)
	}
}

func TestLimitedGroup_Error(t *testing.T) {
	ctx := context.Background()
	lg, _ := LimitGroup(ctx, 2)
	errTest := errors.New("test error")
	var executed int32

	lg.Go(func() error {
		// delay a bit so that other tasks are scheduled.
		time.Sleep(20 * time.Millisecond)
		atomic.AddInt32(&executed, 1)
		return errTest
	})

	// simulates a slower, normal task.
	lg.Go(func() error {
		// wait until cancellation is (hopefully) in effect.
		time.Sleep(50 * time.Millisecond)
		atomic.AddInt32(&executed, 1)
		return nil
	})

	err := lg.Wait()
	if !errors.Is(err, errTest) {
		t.Fatalf("expected error %v, got %v", errTest, err)
	}

	if atomic.LoadInt32(&executed) < 1 {
		t.Errorf("expected at least one task executed, got %d", executed)
	}
}

func TestLimitedGroup_Limit(t *testing.T) {
	ctx := context.Background()
	limit := 2
	lg, _ := LimitGroup(ctx, limit)
	var concurrent, maxConcurrent int32
	numTasks := 10

	for range numTasks {
		lg.Go(func() error {
			// increment the count of concurrently active tasks.
			cur := atomic.AddInt32(&concurrent, 1)
			// update the max concurrent tasks if needed.
			for {
				prevMax := atomic.LoadInt32(&maxConcurrent)
				if cur > prevMax {
					if atomic.CompareAndSwapInt32(&maxConcurrent, prevMax, cur) {
						break
					}
				} else {
					break
				}
			}

			// simulate a short task.
			time.Sleep(20 * time.Millisecond)
			atomic.AddInt32(&concurrent, -1)
			return nil
		})
	}

	if err := lg.Wait(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if maxConcurrent > int32(limit) {
		t.Errorf("expected max concurrent tasks <= %d, got %d", limit, maxConcurrent)
	}
}
