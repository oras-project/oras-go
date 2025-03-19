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

	"golang.org/x/sync/semaphore"
)

func TestLimitedRegion_Success(t *testing.T) {
	limiter := semaphore.NewWeighted(2)
	ctx := context.Background()
	var counter int32

	err := Go(ctx, limiter, func(ctx context.Context, region *LimitedRegion, i int) error {
		// just sleeps a little and increments counter to simulate task
		time.Sleep(10 * time.Millisecond)
		atomic.AddInt32(&counter, 1)
		return nil
	}, 1, 2, 3, 4, 5)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// after everything finishes, we expect counter to be 5
	if want := 5; atomic.LoadInt32(&counter) != int32(want) {
		t.Errorf("expected counter == %v, got %v", want, counter)
	}

	// when all work is done the semaphore should have all permits available
	if !limiter.TryAcquire(2) {
		t.Error("semaphore permits were not fully released at the end")
	}
	limiter.Release(2)
}

func TestLimitedRegion_Cancellation(t *testing.T) {
	limiter := semaphore.NewWeighted(2)
	ctx := context.Background()
	var counter int32

	errTest := errors.New("test error")
	err := Go(ctx, limiter, func(ctx context.Context, region *LimitedRegion, i int) error {
		if i < 0 {
			// trigger an error on negative values
			return errTest
		}
		// just sleeps a little and increments counter to simulate task
		time.Sleep(50 * time.Millisecond)
		atomic.AddInt32(&counter, 1)
		return nil
	}, 1, -1, 2, 0, -2)

	// we expect the returned error to be errTest.
	if !errors.Is(err, errTest) {
		t.Fatalf("expected error %v; got %v", errTest, err)
	}

	// after everything finishes, we expect counter to be smaller than 5.
	if max := 5; atomic.LoadInt32(&counter) >= int32(max) {
		t.Errorf("expected counter < %v, got %v", max, counter)
	}

	// when all work is done the semaphore should have all permits available
	if !limiter.TryAcquire(2) {
		t.Error("semaphore permit was not released after error cancellation")
	}
	limiter.Release(2)
}
