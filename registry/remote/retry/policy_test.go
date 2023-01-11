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

package retry

import (
	"testing"
	"time"
)

func Test_ExponentialBackoff(t *testing.T) {
	testCases := []struct {
		name            string
		attempt         int
		expectedBackoff time.Duration
	}{
		{
			name:    "attempt 0 should have a backoff of 0,25s",
			attempt: 0, expectedBackoff: 250 * time.Millisecond,
		},
		{
			name:    "attempt 1 should have a backoff of 0,5s",
			attempt: 1, expectedBackoff: 500 * time.Millisecond,
		},
		{
			name:    "attempt 2 should have a backoff of 1s",
			attempt: 2, expectedBackoff: 1 * time.Second,
		},
		{
			name:    "attempt 3 should have a backoff of 2s",
			attempt: 3, expectedBackoff: 2 * time.Second,
		},
		{
			name:    "attempt 4 should have a backoff of 4s",
			attempt: 4, expectedBackoff: 4 * time.Second,
		},
		{
			name:    "attempt 5 should have a backoff of 8s",
			attempt: 5, expectedBackoff: 8 * time.Second,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			b := DefaultBackoff(tc.attempt, nil)
			if !(b > tc.expectedBackoff && b <= time.Duration(float64(tc.expectedBackoff)+float64(250*time.Millisecond)*0.1)) {
				t.Errorf("expected backoff to be %s + jitter, got %s", tc.expectedBackoff, b)
			}
		})
	}
}
