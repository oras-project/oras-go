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

// Package retry_test includes all the testable examples for the retry package.
package retry_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/oras-project/oras-go/v3/registry/remote/retry"
)

// ExampleNewClient demonstrates creating an HTTP client with the default retry policy.
func ExampleNewClient() {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	client := retry.NewClient()
	resp, err := client.Get(ts.URL)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()

	fmt.Println(resp.StatusCode)
	// Output:
	// 200
}

// ExampleNewTransport demonstrates wrapping a custom base http.RoundTripper with retry policy.
func ExampleNewTransport() {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	client := &http.Client{
		Transport: retry.NewTransport(http.DefaultTransport),
	}
	resp, err := client.Get(ts.URL)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()

	fmt.Println(resp.StatusCode)
	// Output:
	// 200
}

// ExampleGenericPolicy demonstrates creating a custom retry policy and using it with Transport.
func ExampleGenericPolicy() {
	attempts := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	customPolicy := &retry.GenericPolicy{
		Retryable: func(resp *http.Response, err error) (bool, error) {
			if err != nil {
				return false, err
			}
			return resp.StatusCode == http.StatusServiceUnavailable, nil
		},
		Backoff:  retry.ExponentialBackoff(10*time.Millisecond, 2, 0.1),
		MinWait:  5 * time.Millisecond,
		MaxWait:  100 * time.Millisecond,
		MaxRetry: 3,
	}

	client := &http.Client{
		Transport: &retry.Transport{
			Policy: func() retry.Policy {
				return customPolicy
			},
		},
	}

	resp, err := client.Get(ts.URL)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()

    fmt.Println("attempts:", attempts)
	fmt.Println(resp.StatusCode)
	// Output:
	// 200
}

// ExampleDefaultPolicy demonstrates using DefaultPolicy with a Transport.
func ExampleDefaultPolicy() {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	client := &http.Client{
		Transport: &retry.Transport{
			Policy: func() retry.Policy {
				return retry.DefaultPolicy
			},
		},
	}

	resp, err := client.Get(ts.URL)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()

	fmt.Println(resp.StatusCode)
	// Output:
	// 200
}
