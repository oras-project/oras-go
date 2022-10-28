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

package errutil

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"oras.land/oras-go/v2/registry/remote/remoteerr"
)

func Test_ParseErrorResponse(t *testing.T) {
	path := "/test"
	expectedErrs := remoteerr.ResponseInnerErrors{
		{
			Code:    "UNAUTHORIZED",
			Message: "authentication required",
		},
		{
			Code:    "NAME_UNKNOWN",
			Message: "repository name not known to registry",
		},
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case path:
			msg := `{ "errors": [ { "code": "UNAUTHORIZED", "message": "authentication required", "detail": [ { "Type": "repository", "Class": "", "Name": "library/hello-world", "Action": "pull" } ] }, { "code": "NAME_UNKNOWN", "message": "repository name not known to registry" } ] }`
			w.WriteHeader(http.StatusUnauthorized)
			if _, err := w.Write([]byte(msg)); err != nil {
				t.Errorf("failed to write %q: %v", r.URL, err)
			}
		default:
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}

	}))
	defer ts.Close()

	resp, err := http.Get(ts.URL + path)
	if err != nil {
		t.Fatalf("failed to do request: %v", err)
	}
	err = ParseErrorResponse(resp)
	if err == nil {
		t.Errorf("ParseErrorResponse() error = %v, wantErr %v", err, true)
	}

	var errResp *remoteerr.ErrorResponse
	if ok := errors.As(err, &errResp); !ok {
		t.Errorf("errors.As(err, &UnexpectedStatusCodeError) = %v, want %v", ok, true)
	}
	if want := http.MethodGet; errResp.Method != want {
		t.Errorf("ParseErrorResponse() Method = %v, want Method %v", errResp.Method, want)
	}
	if want := http.StatusUnauthorized; errResp.StatusCode != want {
		t.Errorf("ParseErrorResponse() StatusCode = %v, want StatusCode %v", errResp.StatusCode, want)
	}
	if want := path; errResp.URL.Path != want {
		t.Errorf("ParseErrorResponse() URL = %v, want URL %v", errResp.URL.Path, want)
	}
	for i, e := range errResp.InnerErrors {
		if want := expectedErrs[i].Code; e.Code != expectedErrs[i].Code {
			t.Errorf("ParseErrorResponse() Code = %v, want Code %v", e.Code, want)
		}
		if want := expectedErrs[i].Message; e.Message != want {
			t.Errorf("ParseErrorResponse() Message = %v, want Code %v", e.Code, want)
		}
	}

	errmsg := err.Error()
	if want := "401"; !strings.Contains(errmsg, want) {
		t.Errorf("ParseErrorResponse() error = %v, want err message %v", err, want)
	}
	// first error
	if want := "unauthorized"; !strings.Contains(errmsg, want) {
		t.Errorf("ParseErrorResponse() error = %v, want err message %v", err, want)
	}
	if want := "authentication required"; !strings.Contains(errmsg, want) {
		t.Errorf("ParseErrorResponse() error = %v, want err message %v", err, want)
	}
	// second error
	if want := "name unknown"; !strings.Contains(errmsg, want) {
		t.Errorf("ParseErrorResponse() error = %v, want err message %v", err, want)
	}
	if want := "repository name not known to registry"; !strings.Contains(errmsg, want) {
		t.Errorf("ParseErrorResponse() error = %v, want err message %v", err, want)
	}
}

func Test_ParseErrorResponse_plain(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer ts.Close()

	resp, err := http.Get(ts.URL)
	if err != nil {
		t.Fatalf("failed to do request: %v", err)
	}
	err = ParseErrorResponse(resp)
	if err == nil {
		t.Errorf("ParseErrorResponse() error = %v, wantErr %v", err, true)
	}
	errmsg := err.Error()
	if want := "401"; !strings.Contains(errmsg, want) {
		t.Errorf("ParseErrorResponse() error = %v, want err message %v", err, want)
	}
	if want := http.StatusText(http.StatusUnauthorized); !strings.Contains(errmsg, want) {
		t.Errorf("ParseErrorResponse() error = %v, want err message %v", err, want)
	}
}
