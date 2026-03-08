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

package remote

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// roundTripFunc is an http.RoundTripper backed by a function.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestNewLoggingTransport_Defaults(t *testing.T) {
	lt := NewLoggingTransport(nil, nil)
	if lt.inner == nil {
		t.Error("inner should default to http.DefaultTransport")
	}
	if lt.logger == nil {
		t.Error("logger should default to slog.Default()")
	}
}

func TestLoggingTransport_RoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()

	lt := NewLoggingTransport(http.DefaultTransport, slog.Default())
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	resp, err := lt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip() error = %v", err)
	}
	defer resp.Body.Close()

	// Body must still be readable after LoggingTransport consumed it for logging.
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if string(body) != `{"status":"ok"}` {
		t.Errorf("body = %q, want %q", body, `{"status":"ok"}`)
	}
}

func TestLoggingTransport_RoundTrip_Error(t *testing.T) {
	wantErr := io.ErrUnexpectedEOF
	inner := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, wantErr
	})
	lt := NewLoggingTransport(inner, slog.Default())
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	_, err := lt.RoundTrip(req)
	if err != wantErr {
		t.Errorf("RoundTrip() error = %v, want %v", err, wantErr)
	}
}

func TestFormatHeaders_Scrubbing(t *testing.T) {
	h := http.Header{
		"Authorization": {"Bearer secret"},
		"Content-Type":  {"application/json"},
		"Set-Cookie":    {"session=abc"},
	}
	out := formatHeaders(h)
	if strings.Contains(out, "secret") {
		t.Error("Authorization value should be scrubbed")
	}
	if strings.Contains(out, "session=abc") {
		t.Error("Set-Cookie value should be scrubbed")
	}
	if !strings.Contains(out, "Content-Type") {
		t.Error("Content-Type should be present")
	}
}

func TestFormatHeaders_Empty(t *testing.T) {
	out := formatHeaders(http.Header{})
	if !strings.Contains(out, "Empty") {
		t.Errorf("expected empty header message, got %q", out)
	}
}

func TestFormatResponseBody_NoBody(t *testing.T) {
	resp := &http.Response{Body: http.NoBody}
	out := formatResponseBody(resp)
	if !strings.Contains(out, "No response body") {
		t.Errorf("got %q", out)
	}
}

func TestFormatResponseBody_NonPrintable(t *testing.T) {
	resp := &http.Response{
		Header: http.Header{"Content-Type": {"application/octet-stream"}},
		Body:   io.NopCloser(strings.NewReader("binary")),
	}
	out := formatResponseBody(resp)
	if !strings.Contains(out, "not printed") {
		t.Errorf("got %q", out)
	}
}

func TestFormatResponseBody_CredentialRedaction(t *testing.T) {
	resp := &http.Response{
		Header: http.Header{"Content-Type": {"application/json"}},
		Body:   io.NopCloser(strings.NewReader(`{"token":"secret"}`)),
	}
	out := formatResponseBody(resp)
	if !strings.Contains(out, "redacted") {
		t.Errorf("got %q", out)
	}
}

func TestFormatResponseBody_BodyRestored(t *testing.T) {
	payload := `{"foo":"bar"}`
	resp := &http.Response{
		Header: http.Header{"Content-Type": {"application/json"}},
		Body:   io.NopCloser(strings.NewReader(payload)),
	}
	formatResponseBody(resp)

	// Body must be intact after formatResponseBody reads it.
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll after formatResponseBody: %v", err)
	}
	if string(got) != payload {
		t.Errorf("body after logging = %q, want %q", got, payload)
	}
}

func TestFormatResponseBody_Truncated(t *testing.T) {
	large := strings.Repeat("a", int(loggingPayloadSizeLimit)+100)
	resp := &http.Response{
		Header: http.Header{"Content-Type": {"text/plain"}},
		Body:   io.NopCloser(strings.NewReader(large)),
	}
	out := formatResponseBody(resp)
	if !strings.Contains(out, "truncated") {
		t.Errorf("expected truncation notice, got %q", out[:50])
	}
}

func TestIsLoggableContentType(t *testing.T) {
	tests := []struct {
		ct   string
		want bool
	}{
		{"application/json", true},
		{"application/json; charset=utf-8", true},
		{"application/vnd.oci.image.manifest.v1+json", true},
		{"text/plain", true},
		{"text/html", true},
		{"application/octet-stream", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := isLoggableContentType(tt.ct); got != tt.want {
			t.Errorf("isLoggableContentType(%q) = %v, want %v", tt.ct, got, tt.want)
		}
	}
}

func TestContainsCredentialFields(t *testing.T) {
	if !containsCredentialFields(`{"token":"x"}`) {
		t.Error(`expected true for "token"`)
	}
	if !containsCredentialFields(`{"access_token":"x"}`) {
		t.Error(`expected true for "access_token"`)
	}
	if containsCredentialFields(`{"status":"ok"}`) {
		t.Error("expected false for non-credential body")
	}
}
