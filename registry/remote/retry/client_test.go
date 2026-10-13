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
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync/atomic"
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

func Test_Client_RetryOnConnectionReset(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a reset surfaces as WSAECONNRESET on windows, which is not retried")
	}
	// the two attempts are served on different connections, hence the atomic
	var count atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if count.Add(1) > 1 {
			w.WriteHeader(http.StatusOK)
			return
		}
		// reset the fresh connection instead of answering, which net/http
		// does not retry on its own
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Errorf("failed to hijack the connection: %v", err)
			return
		}
		// a zero linger makes Close send a RST instead of a FIN
		if err := conn.(*net.TCPConn).SetLinger(0); err != nil {
			t.Errorf("failed to set linger: %v", err)
		}
		conn.Close()
	}))
	defer ts.Close()

	resp, err := DefaultClient.Get(ts.URL)
	if err != nil {
		t.Fatalf("failed to do test request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status code %d, got %d", http.StatusOK, resp.StatusCode)
	}
	if n := count.Load(); n != 2 {
		t.Errorf("expected attempts 2, got %d", n)
	}
}
