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
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func Test_Client(t *testing.T) {
	testCases := []struct {
		name        string
		attempts    int
		retryAfter  bool
		StatusCode  int
		expectedErr bool
	}{
		{
			name:     "successful request with 0 retry",
			attempts: 1, retryAfter: false, StatusCode: http.StatusOK, expectedErr: false,
		},
		{
			name: "successful request with 1 retry caused by rate limit",
			// 1 request + 1 retry = 2 attempts
			attempts: 2, retryAfter: true, StatusCode: http.StatusTooManyRequests, expectedErr: false,
		},
		{
			name: "successful request with 1 retry caused by 408",
			// 1 request + 1 retry = 2 attempts
			attempts: 2, retryAfter: false, StatusCode: http.StatusRequestTimeout, expectedErr: false,
		},
		{
			name: "successful request with 2 retries caused by 429",
			// 1 request + 2 retries = 3 attempts
			attempts: 3, retryAfter: false, StatusCode: http.StatusTooManyRequests, expectedErr: false,
		},
		{
			name: "unsuccessful request with 6 retries caused by too many retries",
			// 1 request + 6 retries = 7 attempts
			attempts: 7, retryAfter: false, StatusCode: http.StatusServiceUnavailable, expectedErr: true,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			count := 0
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count++
				if count < tc.attempts {
					if tc.retryAfter {
						w.Header().Set("Retry-After", "1")
					}
					http.Error(w, "error", tc.StatusCode)
					return
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer ts.Close()

			req, err := http.NewRequest(http.MethodPost, ts.URL, bytes.NewReader([]byte("test")))
			if err != nil {
				t.Fatalf("failed to create test request: %v", err)
			}

			resp, err := DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("failed to do test request: %v", err)
			}
			if tc.expectedErr {
				if count != (tc.attempts - 1) {
					t.Errorf("expected attempts %d, got %d", tc.attempts, count)
				}
				if resp.StatusCode != http.StatusServiceUnavailable {
					t.Errorf("expected status code %d, got %d", http.StatusServiceUnavailable, resp.StatusCode)
				}
				return
			}
			if tc.attempts != count {
				t.Errorf("expected attempts %d, got %d", tc.attempts, count)
			}
		})
	}
}
