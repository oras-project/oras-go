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

package retry_test

import (
	"fmt"
	"net/http"
	"time"

	"github.com/oras-project/oras-go/v3/registry/remote/retry"
)

// ExampleNewClient demonstrates creating an HTTP client with the default
// retry policy, suitable for direct use with registry HTTP requests.
func ExampleNewClient() {
	client := retry.NewClient()
	fmt.Println(client != nil)

	// Output:
	// true
}

// ExampleNewTransport demonstrates wrapping an existing HTTP transport with
// retry logic. This is useful when you need to add retries to a transport
// that already has custom TLS or proxy settings.
func ExampleNewTransport() {
	base := &http.Transport{
		MaxIdleConns: 10,
	}
	transport := retry.NewTransport(base)
	client := &http.Client{Transport: transport}
	fmt.Println(client != nil)

	// Output:
	// true
}

// ExampleGenericPolicy demonstrates configuring a custom retry policy with
// specific backoff parameters and a custom predicate.
func ExampleGenericPolicy() {
	policy := &retry.GenericPolicy{
		Retryable: func(resp *http.Response, err error) (bool, error) {
			if err != nil {
				return false, err
			}
			// Only retry on 429 and 503.
			return resp.StatusCode == http.StatusTooManyRequests ||
				resp.StatusCode == http.StatusServiceUnavailable, nil
		},
		Backoff:  retry.ExponentialBackoff(100*time.Millisecond, 2, 0.1),
		MinWait:  100 * time.Millisecond,
		MaxWait:  10 * time.Second,
		MaxRetry: 3,
	}

	transport := &retry.Transport{
		Policy: func() retry.Policy { return policy },
	}
	client := &http.Client{Transport: transport}
	fmt.Println(client != nil)

	// Output:
	// true
}
