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

package remote

import (
	"errors"
	"sync"
	"testing"
)

func TestNewReferrerCapability(t *testing.T) {
	cap := NewReferrerCapability()
	if !cap.IsUnknown() {
		t.Error("NewReferrerCapability() should start with unknown state")
	}
	if cap.IsSupported() {
		t.Error("NewReferrerCapability() should not be supported")
	}
	if cap.IsUnsupported() {
		t.Error("NewReferrerCapability() should not be unsupported")
	}
}

func TestReferrerCapability_SetSupported(t *testing.T) {
	cap := NewReferrerCapability()

	err := cap.SetSupported()
	if err != nil {
		t.Fatalf("SetSupported() error = %v", err)
	}
	if !cap.IsSupported() {
		t.Error("IsSupported() = false after SetSupported()")
	}
	if cap.IsUnknown() {
		t.Error("IsUnknown() = true after SetSupported()")
	}
	if cap.IsUnsupported() {
		t.Error("IsUnsupported() = true after SetSupported()")
	}

	// Setting again to same value should succeed
	err = cap.SetSupported()
	if err != nil {
		t.Errorf("SetSupported() again error = %v", err)
	}
}

func TestReferrerCapability_SetUnsupported(t *testing.T) {
	cap := NewReferrerCapability()

	err := cap.SetUnsupported()
	if err != nil {
		t.Fatalf("SetUnsupported() error = %v", err)
	}
	if cap.IsSupported() {
		t.Error("IsSupported() = true after SetUnsupported()")
	}
	if cap.IsUnknown() {
		t.Error("IsUnknown() = true after SetUnsupported()")
	}
	if !cap.IsUnsupported() {
		t.Error("IsUnsupported() = false after SetUnsupported()")
	}

	// Setting again to same value should succeed
	err = cap.SetUnsupported()
	if err != nil {
		t.Errorf("SetUnsupported() again error = %v", err)
	}
}

func TestReferrerCapability_CannotChangeAfterSet(t *testing.T) {
	// Test setting to supported then trying to set to unsupported
	t.Run("supported then unsupported", func(t *testing.T) {
		cap := NewReferrerCapability()
		if err := cap.SetSupported(); err != nil {
			t.Fatalf("SetSupported() error = %v", err)
		}
		err := cap.SetUnsupported()
		if !errors.Is(err, ErrReferrersCapabilityAlreadySet) {
			t.Errorf("SetUnsupported() error = %v, want %v", err, ErrReferrersCapabilityAlreadySet)
		}
	})

	// Test setting to unsupported then trying to set to supported
	t.Run("unsupported then supported", func(t *testing.T) {
		cap := NewReferrerCapability()
		if err := cap.SetUnsupported(); err != nil {
			t.Fatalf("SetUnsupported() error = %v", err)
		}
		err := cap.SetSupported()
		if !errors.Is(err, ErrReferrersCapabilityAlreadySet) {
			t.Errorf("SetSupported() error = %v, want %v", err, ErrReferrersCapabilityAlreadySet)
		}
	})
}

func TestReferrerCapability_TrySetSupported(t *testing.T) {
	cap := NewReferrerCapability()

	// First try should succeed
	if !cap.TrySetSupported() {
		t.Error("TrySetSupported() = false, want true")
	}
	if !cap.IsSupported() {
		t.Error("IsSupported() = false after TrySetSupported()")
	}

	// Second try with same value should succeed
	if !cap.TrySetSupported() {
		t.Error("TrySetSupported() again = false, want true")
	}

	// Try setting to unsupported should fail
	if cap.TrySetUnsupported() {
		t.Error("TrySetUnsupported() = true after TrySetSupported()")
	}
}

func TestReferrerCapability_TrySetUnsupported(t *testing.T) {
	cap := NewReferrerCapability()

	// First try should succeed
	if !cap.TrySetUnsupported() {
		t.Error("TrySetUnsupported() = false, want true")
	}
	if !cap.IsUnsupported() {
		t.Error("IsUnsupported() = false after TrySetUnsupported()")
	}

	// Second try with same value should succeed
	if !cap.TrySetUnsupported() {
		t.Error("TrySetUnsupported() again = false, want true")
	}

	// Try setting to supported should fail
	if cap.TrySetSupported() {
		t.Error("TrySetSupported() = true after TrySetUnsupported()")
	}
}

func TestReferrerCapability_Reset(t *testing.T) {
	cap := NewReferrerCapability()

	// Set to supported
	if err := cap.SetSupported(); err != nil {
		t.Fatalf("SetSupported() error = %v", err)
	}
	if !cap.IsSupported() {
		t.Error("IsSupported() = false after SetSupported()")
	}

	// Reset
	cap.Reset()
	if !cap.IsUnknown() {
		t.Error("IsUnknown() = false after Reset()")
	}
	if cap.IsSupported() {
		t.Error("IsSupported() = true after Reset()")
	}

	// Should be able to set again after reset
	if err := cap.SetUnsupported(); err != nil {
		t.Errorf("SetUnsupported() after Reset() error = %v", err)
	}
	if !cap.IsUnsupported() {
		t.Error("IsUnsupported() = false after SetUnsupported()")
	}
}

func TestReferrerCapability_State(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(*ReferrerCapability)
		wantState referrersState
	}{
		{
			name:      "unknown",
			setup:     func(c *ReferrerCapability) {},
			wantState: referrersStateUnknown,
		},
		{
			name: "supported",
			setup: func(c *ReferrerCapability) {
				c.SetSupported()
			},
			wantState: referrersStateSupported,
		},
		{
			name: "unsupported",
			setup: func(c *ReferrerCapability) {
				c.SetUnsupported()
			},
			wantState: referrersStateUnsupported,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cap := NewReferrerCapability()
			tt.setup(cap)
			if got := cap.State(); got != tt.wantState {
				t.Errorf("State() = %v, want %v", got, tt.wantState)
			}
		})
	}
}

func TestReferrerCapability_Concurrent(t *testing.T) {
	cap := NewReferrerCapability()

	var wg sync.WaitGroup
	// Run multiple goroutines trying to set the state
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			cap.TrySetSupported()
		}()
		go func() {
			defer wg.Done()
			cap.TrySetUnsupported()
		}()
	}
	wg.Wait()

	// After all concurrent attempts, exactly one state should be set
	if cap.IsUnknown() {
		t.Error("State should not be unknown after concurrent sets")
	}
	if cap.IsSupported() && cap.IsUnsupported() {
		t.Error("State cannot be both supported and unsupported")
	}
	if !cap.IsSupported() && !cap.IsUnsupported() {
		t.Error("State must be either supported or unsupported")
	}
}

func TestNewReferrerMergePool(t *testing.T) {
	pool := NewReferrerMergePool()
	if pool == nil {
		t.Fatal("NewReferrerMergePool() returned nil")
	}
}

func TestReferrerMergePool_Get(t *testing.T) {
	pool := NewReferrerMergePool()

	// Get should return a merge and a done function
	merge, done := pool.Get("sha256-abc123")
	if merge == nil {
		t.Fatal("Get() returned nil merge")
	}
	if done == nil {
		t.Fatal("Get() returned nil done function")
	}

	// Clean up
	done()
}

func TestReferrerMergePool_Do(t *testing.T) {
	pool := NewReferrerMergePool()

	prepareCalled := false
	updateCalled := false
	var receivedChanges []referrerChange

	err := pool.Do(
		"sha256-abc123",
		referrerChange{operation: referrerOperationAdd},
		func() error {
			prepareCalled = true
			return nil
		},
		func(changes []referrerChange) error {
			updateCalled = true
			receivedChanges = changes
			return nil
		},
	)

	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	if !prepareCalled {
		t.Error("prepare function was not called")
	}
	if !updateCalled {
		t.Error("update function was not called")
	}
	if len(receivedChanges) != 1 {
		t.Errorf("expected 1 change, got %d", len(receivedChanges))
	}
}

func TestReferrerMergePool_Do_PrepareError(t *testing.T) {
	pool := NewReferrerMergePool()

	expectedErr := errors.New("prepare error")

	err := pool.Do(
		"sha256-abc123",
		referrerChange{operation: referrerOperationAdd},
		func() error {
			return expectedErr
		},
		func(changes []referrerChange) error {
			t.Error("update should not be called when prepare fails")
			return nil
		},
	)

	if !errors.Is(err, expectedErr) {
		t.Errorf("Do() error = %v, want %v", err, expectedErr)
	}
}

func TestReferrerMergePool_Do_UpdateError(t *testing.T) {
	pool := NewReferrerMergePool()

	expectedErr := errors.New("update error")

	err := pool.Do(
		"sha256-abc123",
		referrerChange{operation: referrerOperationAdd},
		func() error {
			return nil
		},
		func(changes []referrerChange) error {
			return expectedErr
		},
	)

	if !errors.Is(err, expectedErr) {
		t.Errorf("Do() error = %v, want %v", err, expectedErr)
	}
}

func TestReferrerMergePool_Concurrent(t *testing.T) {
	pool := NewReferrerMergePool()

	var wg sync.WaitGroup
	var prepareCount, updateCount int
	var mu sync.Mutex

	// Run multiple goroutines doing updates on the same tag
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := pool.Do(
				"sha256-same-tag",
				referrerChange{operation: referrerOperationAdd},
				func() error {
					mu.Lock()
					prepareCount++
					mu.Unlock()
					return nil
				},
				func(changes []referrerChange) error {
					mu.Lock()
					updateCount++
					mu.Unlock()
					return nil
				},
			)
			if err != nil {
				t.Errorf("Do() error = %v", err)
			}
		}()
	}

	wg.Wait()

	// Due to merging, prepare and update may be called fewer times than
	// the total number of concurrent operations
	if prepareCount == 0 {
		t.Error("prepare should have been called at least once")
	}
	if updateCount == 0 {
		t.Error("update should have been called at least once")
	}
}
