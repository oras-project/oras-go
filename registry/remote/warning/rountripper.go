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

package warning

import "net/http"

type Transport struct {
	// Base is the underlying HTTP transport to use.
	// If nil, http.DefaultTransport is used for round trips.
	Base           http.RoundTripper
	WarningHandler WarningHandler
}

// RoundTrip executes a single HTTP transaction, returning
// a Response for the provided Request.
//
// RoundTrip should not attempt to interpret the response. In
// particular, RoundTrip must return err == nil if it obtained
// a response, regardless of the response's HTTP status code.
// A non-nil err should be reserved for failure to obtain a
// response. Similarly, RoundTrip should not attempt to
// handle higher-level protocol details such as redirects,
// authentication, or cookies.
//
// RoundTrip should not modify the request, except for
// consuming and closing the Request's Body. RoundTrip may
// read fields of the request in a separate goroutine. Callers
// should not mutate or reuse the request until the Response's
// Body has been closed.
//
// RoundTrip must always close the body, including on errors,
// but depending on the implementation may do so in a separate
// goroutine even after RoundTrip returns. This means that
// callers wanting to reuse the body for subsequent requests
// must arrange to wait for the Close call before doing so.
//
// The Request's URL and Header fields must be initialized.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.roundTrip(req)
	if err != nil {
		return nil, err
	}
	// defer resp.Body.Close()

	// TODO: const
	if warnings := resp.Header["Warning"]; len(warnings) > 0 {
		// TODO: dedup?
		for _, w := range warnings {
			warning := new(*req.URL, w)
			t.WarningHandler.HandleWarning(warning)
		}
	}

	return resp, nil
}

func (t *Transport) roundTrip(req *http.Request) (*http.Response, error) {
	if t.Base == nil {
		return http.DefaultTransport.RoundTrip(req)
	}
	return t.Base.RoundTrip(req)
}
