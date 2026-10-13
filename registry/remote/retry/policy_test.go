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
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"syscall"
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
			name:    "attempt 0 should have a backoff of 0,25s ± 10%",
			attempt: 0, expectedBackoff: 250 * time.Millisecond,
		},
		{
			name:    "attempt 1 should have a backoff of 0,5s ± 10%",
			attempt: 1, expectedBackoff: 500 * time.Millisecond,
		},
		{
			name:    "attempt 2 should have a backoff of 1s ± 10%",
			attempt: 2, expectedBackoff: 1 * time.Second,
		},
		{
			name:    "attempt 3 should have a backoff of 2s ± 10%",
			attempt: 3, expectedBackoff: 2 * time.Second,
		},
		{
			name:    "attempt 4 should have a backoff of 4s ± 10%",
			attempt: 4, expectedBackoff: 4 * time.Second,
		},
		{
			name:    "attempt 5 should have a backoff of 8s ± 10%",
			attempt: 5, expectedBackoff: 8 * time.Second,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			b := DefaultBackoff(tc.attempt, nil)
			base := float64(tc.expectedBackoff)
			if !(b >= time.Duration(base*0.9) && b <= time.Duration(base+base*0.1)) {
				t.Errorf("expected backoff to be %s + jitter, got %s", time.Duration(base), b)
			}
		})
	}
}

func Test_ExponentialBackoff_NoJitter(t *testing.T) {
	// a zero jitter should not panic and should return the exact
	// exponential backoff
	backoff := ExponentialBackoff(250*time.Millisecond, 2, 0)
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
			name:    "attempt 4 should have a backoff of 4s",
			attempt: 4, expectedBackoff: 4 * time.Second,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if b := backoff(tc.attempt, nil); b != tc.expectedBackoff {
				t.Errorf("expected backoff to be %s, got %s", tc.expectedBackoff, b)
			}
		})
	}
}

func Test_DefaultPredicate(t *testing.T) {
	unrelatedErr := errors.New("unrelated error")
	// connection errors wrapped the way net/http returns them
	resetErr := &url.Error{
		Op:  "Get",
		URL: "https://registry.example/v2/repo/tags/list",
		Err: &net.OpError{Op: "read", Net: "tcp", Err: os.NewSyscallError("read", syscall.ECONNRESET)},
	}
	unexpectedEOFErr := &url.Error{
		Op:  "Get",
		URL: "https://registry.example/v2/repo/tags/list",
		Err: io.ErrUnexpectedEOF,
	}
	refusedErr := &url.Error{
		Op:  "Get",
		URL: "https://registry.example/v2/repo/tags/list",
		Err: &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)},
	}
	testCases := []struct {
		name      string
		err       error
		wantRetry bool
		wantErr   error
	}{
		{
			// a reset read on a fresh connection, which net/http does not
			// retry on its own
			name:      "connection reset by peer should be retried",
			err:       resetErr,
			wantRetry: true,
		},
		{
			name:      "unexpected EOF should be retried",
			err:       unexpectedEOFErr,
			wantRetry: true,
		},
		{
			name:    "connection refused should not be retried",
			err:     refusedErr,
			wantErr: refusedErr,
		},
		{
			name:    "an unrelated error should not be retried",
			err:     unrelatedErr,
			wantErr: unrelatedErr,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			retry, err := DefaultPredicate(nil, tc.err)
			if err != tc.wantErr {
				t.Errorf("expected error %v, got %v", tc.wantErr, err)
			}
			if retry != tc.wantRetry {
				t.Errorf("expected retry %v, got %v", tc.wantRetry, retry)
			}
		})
	}
}
