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

// TestLimitedRegionAllSuccess verifies that when no errors occur, all goroutines run to completion
// and the semaphore is properly released.
func TestLimitedRegionAllSuccess(t *testing.T) {
	// Create a semaphore with capacity 2.
	sem := semaphore.NewWeighted(2)
	ctx := context.Background()
	var counter int32

	// Use numbers 1..5; our dummy function just sleeps a little and increments counter.
	err := Go(ctx, sem, func(ctx context.Context, region *LimitedRegion, i int) error {
		// Simulate work.
		time.Sleep(10 * time.Millisecond)
		atomic.AddInt32(&counter, 1)
		return nil
	}, 1, 2, 3, 4, 5)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// After everything finishes, we expect counter to be 5.
	if atomic.LoadInt32(&counter) != 5 {
		t.Errorf("expected counter==5, got %d", counter)
	}

	// When all work is done the semaphore should have all permits available.
	// Try acquiring the full weight—if the permits weren't all released,
	// TryAcquire would return false.
	if !sem.TryAcquire(2) {
		t.Error("semaphore permits were not fully released at the end")
	}
	// Release the permits we just acquired.
	sem.Release(2)
}

// TestLimitedRegionCancellation verifies that if an early error occurs,
// the overall Go call returns the original error (instead of just context.Canceled)
// and semaphore permits are released.
func TestLimitedRegionCancellation(t *testing.T) {
	sem := semaphore.NewWeighted(2)
	ctx := context.Background()
	var counter int32

	// We choose a special item (e.g. value 42) to trigger an error.
	errTest := errors.New("intentional error")
	err := Go(ctx, sem, func(ctx context.Context, region *LimitedRegion, i int) error {
		if i == 42 {
			return errTest
		}
		time.Sleep(50 * time.Millisecond)
		atomic.AddInt32(&counter, 1)
		return nil
	}, 1, 42, 3, 4)

	// We expect the first error to be errTest.
	if err == nil {
		t.Fatal("expected an error, but got nil")
	}
	if !errors.Is(err, errTest) {
		t.Fatalf("expected error %v; got %v", errTest, err)
	}

	// After everything finishes, we expect counter to be smaller than 4.
	if atomic.LoadInt32(&counter) >= 4 {
		t.Errorf("expected counter < 4, got %d", counter)
	}

	// Ensure that the semaphore is fully released.
	if !sem.TryAcquire(2) {
		t.Error("semaphore permit was not released after error cancellation")
	}
	sem.Release(2)
}
