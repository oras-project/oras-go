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
	"sync"
	"testing"
)

func TestNewReferrerMergePool(t *testing.T) {
	pool := newReferrerMergePool()
	if pool == nil {
		t.Fatal("newReferrerMergePool() returned nil")
	}
}

func TestReferrerMergePool_Get(t *testing.T) {
	pool := newReferrerMergePool()

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

func TestReferrerMergePool_Concurrent(t *testing.T) {
	pool := newReferrerMergePool()

	var wg sync.WaitGroup
	var prepareCount, updateCount int
	var mu sync.Mutex

	// Run multiple goroutines doing updates on the same tag
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Same shape as manifestStore.updateReferrersIndex.
			merge, done := pool.Get("sha256-same-tag")
			defer done()
			err := merge.Do(
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
