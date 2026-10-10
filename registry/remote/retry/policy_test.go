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
	"net/http"
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

func Test_ExponentialBackoff_FullJitter(t *testing.T) {
	// a jitter of 1 spreads the backoff over [0, 2*temp)
	backoff := ExponentialBackoff(250*time.Millisecond, 2, 1)
	for range 100 {
		if b := backoff(2, nil); b < 0 || b >= 2*time.Second {
			t.Fatalf("expected backoff in [0s, 2s), got %s", b)
		}
	}
}

func Test_ExponentialBackoff_RetryAfter(t *testing.T) {
	// ExponentialBackoff keeps returning a 429 Retry-After verbatim
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{"Retry-After": []string{"7"}},
	}
	if b := DefaultBackoff(0, resp); b != 7*time.Second {
		t.Errorf("expected backoff to be 7s, got %s", b)
	}

	// other statuses keep using the exponential backoff
	resp.StatusCode = http.StatusServiceUnavailable
	if b := DefaultBackoff(0, resp); b < 225*time.Millisecond || b > 275*time.Millisecond {
		t.Errorf("expected backoff to be 250ms ± 10%%, got %s", b)
	}
}

func Test_GenericPolicy_Retry_NoRetry(t *testing.T) {
	predicateErr := errors.New("predicate error")
	testCases := []struct {
		name    string
		policy  *GenericPolicy
		attempt int
		resp    *http.Response
		err     error
		wantErr error
	}{
		{
			name: "no retry when attempt reaches MaxRetry",
			policy: &GenericPolicy{
				Retryable: DefaultPredicate,
				Backoff:   DefaultBackoff,
				MinWait:   200 * time.Millisecond,
				MaxWait:   3 * time.Second,
				MaxRetry:  5,
			},
			attempt: 5,
		},
		{
			name: "no retry when the predicate rejects",
			policy: &GenericPolicy{
				Retryable: DefaultPredicate,
				Backoff:   DefaultBackoff,
				MinWait:   200 * time.Millisecond,
				MaxWait:   3 * time.Second,
				MaxRetry:  5,
			},
			attempt: 0,
			resp:    &http.Response{StatusCode: http.StatusOK},
		},
		{
			name: "predicate error is returned",
			policy: &GenericPolicy{
				Retryable: func(*http.Response, error) (bool, error) {
					return false, predicateErr
				},
				Backoff:  DefaultBackoff,
				MinWait:  200 * time.Millisecond,
				MaxWait:  3 * time.Second,
				MaxRetry: 5,
			},
			attempt: 0,
			wantErr: predicateErr,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			wait, err := tc.policy.Retry(tc.attempt, tc.resp, tc.err)
			if wait != -1 {
				t.Errorf("expected wait to be -1, got %s", wait)
			}
			if err != tc.wantErr {
				t.Errorf("expected error %v, got %v", tc.wantErr, err)
			}
		})
	}
}

func Test_GenericPolicy_Retry(t *testing.T) {
	respWith := func(status int, retryAfter string) *http.Response {
		r := &http.Response{StatusCode: status, Header: http.Header{}}
		if retryAfter != "" {
			r.Header.Set("Retry-After", retryAfter)
		}
		return r
	}
	base := func() *GenericPolicy {
		return &GenericPolicy{
			Retryable: DefaultPredicate,
			Backoff:   DefaultBackoff,
			MinWait:   200 * time.Millisecond,
			MaxWait:   3 * time.Second,
			MaxRetry:  5,
		}
	}
	future := time.Now().Add(30 * time.Second).UTC().Format(http.TimeFormat)
	farFuture := time.Now().Add(10 * time.Minute).UTC().Format(http.TimeFormat)
	past := time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat)

	testCases := []struct {
		name    string
		policy  *GenericPolicy
		attempt int
		resp    *http.Response
		minWait time.Duration
		maxWait time.Duration
	}{
		{
			name: "429 Retry-After honored past MaxWait and capped at MaxRetryAfter",
			policy: func() *GenericPolicy {
				p := base()
				p.MaxRetryAfter = 60 * time.Second
				return p
			}(),
			attempt: 0,
			resp:    respWith(http.StatusTooManyRequests, "60"),
			minWait: 60 * time.Second,
			maxWait: 60 * time.Second,
		},
		{
			name: "Retry-After capped at MaxRetryAfter",
			policy: func() *GenericPolicy {
				p := base()
				p.MaxRetryAfter = 60 * time.Second
				return p
			}(),
			attempt: 0,
			resp:    respWith(http.StatusTooManyRequests, "100"),
			minWait: 60 * time.Second,
			maxWait: 60 * time.Second,
		},
		{
			name: "Retry-After below cap gets additive jitter and is never below the server figure",
			policy: func() *GenericPolicy {
				p := base()
				p.MaxRetryAfter = 60 * time.Second
				return p
			}(),
			attempt: 0,
			resp:    respWith(http.StatusTooManyRequests, "2"),
			minWait: 2 * time.Second,
			maxWait: 2200 * time.Millisecond,
		},
		{
			name:    "MaxRetryAfter zero falls back to MaxWait",
			policy:  base(),
			attempt: 0,
			resp:    respWith(http.StatusTooManyRequests, "60"),
			minWait: 3 * time.Second,
			maxWait: 3 * time.Second,
		},
		{
			name: "503 with Retry-After honored",
			policy: func() *GenericPolicy {
				p := base()
				p.MaxRetryAfter = 60 * time.Second
				return p
			}(),
			attempt: 0,
			resp:    respWith(http.StatusServiceUnavailable, "2"),
			minWait: 2 * time.Second,
			maxWait: 2200 * time.Millisecond,
		},
		{
			name: "HTTP-date form honored",
			policy: func() *GenericPolicy {
				p := base()
				p.MaxRetryAfter = 60 * time.Second
				return p
			}(),
			attempt: 0,
			resp:    respWith(http.StatusTooManyRequests, future),
			minWait: 29 * time.Second,
			maxWait: 33 * time.Second,
		},
		{
			name: "far-future HTTP-date capped at MaxRetryAfter",
			policy: func() *GenericPolicy {
				p := base()
				p.MaxRetryAfter = 60 * time.Second
				return p
			}(),
			attempt: 0,
			resp:    respWith(http.StatusTooManyRequests, farFuture),
			minWait: 60 * time.Second,
			maxWait: 60 * time.Second,
		},
		{
			name: "past HTTP-date falls back to the backoff",
			policy: func() *GenericPolicy {
				p := base()
				p.MaxRetryAfter = 60 * time.Second
				return p
			}(),
			attempt: 0,
			resp:    respWith(http.StatusTooManyRequests, past),
			minWait: 225 * time.Millisecond,
			maxWait: 275 * time.Millisecond,
		},
		{
			name: "unparseable Retry-After falls back to the backoff",
			policy: func() *GenericPolicy {
				p := base()
				p.MaxRetryAfter = 60 * time.Second
				return p
			}(),
			attempt: 0,
			resp:    respWith(http.StatusTooManyRequests, "soon"),
			minWait: 225 * time.Millisecond,
			maxWait: 275 * time.Millisecond,
		},
		{
			name: "zero Retry-After falls back to the backoff",
			policy: func() *GenericPolicy {
				p := base()
				p.MaxRetryAfter = 60 * time.Second
				return p
			}(),
			attempt: 0,
			resp:    respWith(http.StatusTooManyRequests, "0"),
			minWait: 225 * time.Millisecond,
			maxWait: 275 * time.Millisecond,
		},
		{
			name: "MinWait floors a small Retry-After",
			policy: func() *GenericPolicy {
				p := base()
				p.MinWait = 5 * time.Second
				p.MaxRetryAfter = 60 * time.Second
				return p
			}(),
			attempt: 0,
			resp:    respWith(http.StatusTooManyRequests, "1"),
			minWait: 5 * time.Second,
			maxWait: 5 * time.Second,
		},
		{
			name: "delta-seconds that overflows when jitter is added capped at MaxRetryAfter",
			policy: func() *GenericPolicy {
				p := base()
				p.MaxRetryAfter = 60 * time.Second
				return p
			}(),
			attempt: 0,
			resp:    respWith(http.StatusTooManyRequests, "9223372036"),
			minWait: 60 * time.Second,
			maxWait: 60 * time.Second,
		},
		{
			name: "delta-seconds that overflows time.Duration capped at MaxRetryAfter",
			policy: func() *GenericPolicy {
				p := base()
				p.MaxRetryAfter = 60 * time.Second
				return p
			}(),
			attempt: 0,
			resp:    respWith(http.StatusTooManyRequests, "9223372037"),
			minWait: 60 * time.Second,
			maxWait: 60 * time.Second,
		},
		{
			name: "delta-seconds that wraps time.Duration to a short wait capped at MaxRetryAfter",
			policy: func() *GenericPolicy {
				p := base()
				p.MaxRetryAfter = 60 * time.Second
				return p
			}(),
			attempt: 0,
			resp:    respWith(http.StatusTooManyRequests, "18446744074"),
			minWait: 60 * time.Second,
			maxWait: 60 * time.Second,
		},
		{
			name: "far-future HTTP-date where time.Until saturates capped at MaxRetryAfter",
			policy: func() *GenericPolicy {
				p := base()
				p.MaxRetryAfter = 60 * time.Second
				return p
			}(),
			attempt: 0,
			resp:    respWith(http.StatusTooManyRequests, "Fri, 31 Dec 9999 23:59:59 GMT"),
			minWait: 60 * time.Second,
			maxWait: 60 * time.Second,
		},
		{
			name:   "backoff clamped to MaxWait",
			policy: base(),
			resp:   &http.Response{StatusCode: http.StatusServiceUnavailable},
			// attempt 4 would back off for 4s ± 10%, clamped to MaxWait
			attempt: 4,
			minWait: 3 * time.Second,
			maxWait: 3 * time.Second,
		},
		{
			name: "backoff floored at MinWait",
			policy: func() *GenericPolicy {
				p := base()
				p.MinWait = 500 * time.Millisecond
				return p
			}(),
			attempt: 0,
			resp:    &http.Response{StatusCode: http.StatusServiceUnavailable},
			minWait: 500 * time.Millisecond,
			maxWait: 500 * time.Millisecond,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// iterate so that bounds hold for every jitter outcome
			for range 100 {
				wait, err := tc.policy.Retry(tc.attempt, tc.resp, nil)
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if wait < tc.minWait || wait > tc.maxWait {
					t.Fatalf("expected wait in [%s, %s], got %s", tc.minWait, tc.maxWait, wait)
				}
			}
		})
	}
}
