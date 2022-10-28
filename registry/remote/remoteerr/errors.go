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
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"unicode"
)

// maxErrorBytes specifies the default limit on how many response bytes are
// allowed in the server's error response.
// A typical error message is around 200 bytes. Hence, 8 KiB should be
// sufficient.
const maxErrorBytes int64 = 8 * 1024 // 8 KiB

// ResponseError represent a response error returned by the remote registry.
type ResponseError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Detail  any    `json:"detail"`
}

// Error returns a error string describing the error.
func (e ResponseError) Error() string {
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

// ResponseErrors represent a list of response errors returned by the remote
// server.
type ResponseErrors []ResponseError

// Error returns a error string describing the error.
func (errs ResponseErrors) Error() string {
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

// ErrorResponse represent an error response.
type ErrorResponse struct {
	Method      string
	URL         *url.URL
	StatusCode  int
	InnerErrors ResponseErrors
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

// ParseErrorResponse parses the error returned by the remote registry.
func ParseErrorResponse(resp *http.Response) error {
	resultErr := &ErrorResponse{
		Method:     resp.Request.Method,
		URL:        resp.Request.URL,
		StatusCode: resp.StatusCode,
	}
	var body struct {
		Errors ResponseErrors `json:"errors"`
	}
	lr := io.LimitReader(resp.Body, maxErrorBytes)
	if err := json.NewDecoder(lr).Decode(&body); err != nil {
		return resultErr
	}

	resultErr.InnerErrors = body.Errors
	return resultErr
}
