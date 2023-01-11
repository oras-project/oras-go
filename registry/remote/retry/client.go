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
	"io"
	"net/http"
	"time"
)

// // DefaultClient is a client with the default retry policy
var DefaultClient = NewClient()

// NewClient creates an HTTP client with the default retry policy
func NewClient() *http.Client {
	return &http.Client{
		Transport: NewTransport(nil),
	}
}

// Transport is an HTTP transport with retry policy
type Transport struct {
	// Base is the underlying HTTP transport to use.
	// If nil, http.DefaultTransport is used for round trips.
	Base http.RoundTripper

	// Policy returns a retry Policy to use for the request.
	// If nil, DefaultPolicy is used to determine if the request should be retried.
	Policy func() Policy
}

// NewTransport creates an HTTP Transport with the default retry policy
func NewTransport(base http.RoundTripper) *Transport {
	return &Transport{
		Base: base,
	}
}

// RoundTrip executes a single HTTP transaction, returning a Response for the provided Request.
// It relies on the configured Policy to determine if the request should be retried and to backoff.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	policy := t.policy()
	attempt := 0
	for {
		resp, respErr := t.roundTrip(req)
		duration, err := policy.Retry(ctx, attempt, resp, respErr)
		if duration < 0 {
			return resp, err
		}

		// rewind the body if necessary
		if req.Body != nil {
			var buf bytes.Buffer
			if _, err = buf.ReadFrom(resp.Body); err != nil {
				return resp, err
			}
			if err = resp.Body.Close(); err != nil {
				return resp, err
			}
			req.Body = io.NopCloser(&buf)
		}

		timer := time.NewTimer(duration)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
		attempt++
	}
}

func (t *Transport) roundTrip(req *http.Request) (*http.Response, error) {
	if t.Base == nil {
		return http.DefaultTransport.RoundTrip(req)
	}
	return t.Base.RoundTrip(req)
}

func (t *Transport) policy() Policy {
	if t.Policy == nil {
		return DefaultPolicy
	}
	return t.Policy()
}
