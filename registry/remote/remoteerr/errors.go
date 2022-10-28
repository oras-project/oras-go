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

package remoteerr

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode"
)

// ResponseInnerError represents a response inner error returned by the remote
// registry.
type ResponseInnerError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Detail  any    `json:"detail"`
}

// Error returns a error string describing the error.
func (e ResponseInnerError) Error() string {
	code := strings.Map(func(r rune) rune {
		if r == '_' {
			return ' '
		}
		return unicode.ToLower(r)
	}, e.Code)
	if e.Message == "" {
		return code
	}
	return fmt.Sprintf("%s: %s: %v", code, e.Message, e.Detail)
}

// ResponseInnerErrors represents a list of response inner errors returned by
// the remote server.
type ResponseInnerErrors []ResponseInnerError

// Error returns a error string describing the error.
func (errs ResponseInnerErrors) Error() string {
	switch len(errs) {
	case 0:
		return "<nil>"
	case 1:
		return errs[0].Error()
	}
	var errmsgs []string
	for _, err := range errs {
		errmsgs = append(errmsgs, err.Error())
	}
	return strings.Join(errmsgs, "; ")
}

// ErrorResponse represents an error response.
type ErrorResponse struct {
	Method      string
	URL         *url.URL
	StatusCode  int
	InnerErrors ResponseInnerErrors
}

// Error returns a error string describing the error.
func (err *ErrorResponse) Error() string {
	var errmsg string
	if len(err.InnerErrors) > 0 {
		errmsg = err.InnerErrors.Error()
	} else {
		errmsg = http.StatusText(err.StatusCode)
	}
	return fmt.Sprintf("%s %q: response status code %d: %s", err.Method, err.URL, err.StatusCode, errmsg)
}
