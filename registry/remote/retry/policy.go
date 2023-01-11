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
	"context"
	"fmt"
	"math"
	"math/rand"
	"net/http"
	"strconv"
	"time"
)

// headerRetryAfter is the header key for Retry-After.
const headerRetryAfter = "Retry-After"

// DefaultPolicy is a policy with fine-tuned retry parameters.
// It uses an exponential backoff with jitter.
var DefaultPolicy Policy = &GenericPolicy{
	Retryable: DefaultPredicate,
	Backoff:   DefaultBackoff,
	MinWait:   200 * time.Millisecond,
	MaxWait:   3 * time.Second,
	MaxRetry:  5,
}

// DefaultPredicate is a predicate that retries on 5xx errors, 429 Too Many
// Requests, 401 Unauthorized and 408 Request Timeout.
var DefaultPredicate Predicate = func(ctx context.Context, resp *http.Response, err error) (bool, error) {
	if ctx.Err() != nil {
		return false, ctx.Err()
	}

	if err != nil {
		return false, err
	}

	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return true, nil
	case http.StatusRequestTimeout, http.StatusTooManyRequests:
		return true, nil
	}

	if resp.StatusCode == 0 || resp.StatusCode >= 500 {
		return true, fmt.Errorf("unexpected HTTP status %s", resp.Status)
	}

	return false, nil
}

// DefaultBackoff is a backoff that uses an exponential backoff with jitter.
// It uses a base of 250ms, a factor of 2 and a jitter of 10%.
var DefaultBackoff Backoff = ExponentialBackoff(250*time.Millisecond, 2, 0.1)

// Policy is a retry policy.
type Policy interface {
	// Retry returns the duration to wait before retrying the request.
	Retry(ctx context.Context, attempt int, resp *http.Response, err error) (time.Duration, error)
}

// Predicate is a function that returns true if the request should be retried.
type Predicate func(ctx context.Context, resp *http.Response, err error) (bool, error)

// Backoff is a function that returns the duration to wait before retrying the
// request. The attempt, is the next attempt number. The response is the
// response from the previous request.
type Backoff func(attempt int, resp *http.Response) time.Duration

// ExponentialBackoff returns a Backoff that uses an exponential backoff with
// jitter. The backoff is calculated as:
//
//	backoff * factor ^ attempt + rand.Int63n(jitter * backoff)
//
// The HTTP response is checked for a Retry-After header. If it is present, the
// value is used as the backoff duration and jitter is applied.
func ExponentialBackoff(backoff time.Duration, factor int, jitter float64) Backoff {
	return func(attempt int, resp *http.Response) time.Duration {
		// Seed random number generator with nanoseconds
		rand := rand.New(rand.NewSource(int64(time.Now().Nanosecond())))
		// check Retry-After
		if resp != nil && resp.StatusCode == http.StatusTooManyRequests {
			if v := resp.Header.Get(headerRetryAfter); v != "" {
				if retryAfter, _ := strconv.ParseInt(v, 10, 64); retryAfter > 0 {
					return time.Duration(retryAfter + rand.Int63n(int64(jitter*float64(backoff))))
				}
			}
		}

		// do exponential backoff with jitter
		b := time.Duration(float64(backoff) * math.Pow(float64(factor), float64(attempt)))
		return b + time.Duration(rand.Int63n(int64(jitter*float64(backoff))))
	}
}

// GenericPolicy is a generic retry policy.
type GenericPolicy struct {
	// Retryable is a predicate that returns true if the request should be
	// retried.
	Retryable Predicate

	// Backoff is a function that returns the duration to wait before retrying.
	Backoff Backoff

	// MinWait is the minimum duration to wait before retrying.
	MinWait time.Duration

	// MaxWait is the maximum duration to wait before retrying.
	MaxWait time.Duration

	// MaxRetry is the maximum number of retries.
	MaxRetry int
}

// Retry returns the duration to wait before retrying the request.
// It returns -1 if the request should not be retried.
func (p *GenericPolicy) Retry(ctx context.Context, attempt int, resp *http.Response, err error) (time.Duration, error) {
	if attempt >= p.MaxRetry {
		return -1, err
	}
	if ok, err := p.Retryable(ctx, resp, err); !ok {
		return -1, err
	}
	backoff := p.Backoff(attempt, resp)
	if backoff < p.MinWait {
		backoff = p.MinWait
	}
	if backoff > p.MaxWait {
		backoff = p.MaxWait
	}
	return backoff, nil
}
