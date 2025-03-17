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
// (a) the overall Go call returns the error and (b) semaphore permits are released.
func TestLimitedRegionCancellation(t *testing.T) {
	sem := semaphore.NewWeighted(2)
	ctx := context.Background()
	var counter int32

	// We choose a special item (e.g. value 42) to trigger error.
	errTest := errors.New("intentional error")
	err := Go(ctx, sem, func(ctx context.Context, region *LimitedRegion, i int) error {
		// If we see the trigger value, return an error immediately.
		if i == 42 {
			return errTest
		}
		// Otherwise, simulate some work that takes time.
		time.Sleep(50 * time.Millisecond)
		atomic.AddInt32(&counter, 1)
		return nil
	}, 1, 42, 3, 4)

	// In our design the first error should cancel work and be returned.
	if err == nil {
		t.Fatal("expected an error, but got nil")
	}
	if !errors.Is(err, errTest) {
		t.Fatalf("expected error %v; got %v", errTest, err)
	}

	// Some of the non-error tasks may have been short-circuited.
	// Regardless, once Go returns the semaphore should be fully released.
	if !sem.TryAcquire(2) {
		t.Error("semaphore permit was not released after error cancellation")
	}
	sem.Release(2)
}
