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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func Test_ParseErrorResponse(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		msg := `{"errors":[{"code":"UNAUTHORIZED","message":"authentication required","detail":[{"Type":"repository","Class":"","Name":"library/hello-world","Action":"pull"}]}]}`
		w.WriteHeader(http.StatusUnauthorized)
		if _, err := w.Write([]byte(msg)); err != nil {
			t.Errorf("failed to write %q: %v", r.URL, err)
		}
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
	if want := "unauthorized"; !strings.Contains(errmsg, want) {
		t.Errorf("ParseErrorResponse() error = %v, want err message %v", err, want)
	}
	if want := "authentication required"; !strings.Contains(errmsg, want) {
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
