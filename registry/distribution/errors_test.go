package distribution

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func Test_parseErrorResponse(t *testing.T) {
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
	err = parseErrorResponse(resp)
	if err == nil {
		t.Errorf("parseErrorResponse() error = %v, wantErr %v", err, true)
	}
	errmsg := err.Error()
	if want := "401"; !strings.Contains(errmsg, want) {
		t.Errorf("parseErrorResponse() error = %v, want err message %v", err, want)
	}
	if want := "unauthorized"; !strings.Contains(errmsg, want) {
		t.Errorf("parseErrorResponse() error = %v, want err message %v", err, want)
	}
	if want := "authentication required"; !strings.Contains(errmsg, want) {
		t.Errorf("parseErrorResponse() error = %v, want err message %v", err, want)
	}
}

func Test_parseErrorResponse_plain(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer ts.Close()

	resp, err := http.Get(ts.URL)
	if err != nil {
		t.Fatalf("failed to do request: %v", err)
	}
	err = parseErrorResponse(resp)
	if err == nil {
		t.Errorf("parseErrorResponse() error = %v, wantErr %v", err, true)
	}
	errmsg := err.Error()
	if want := "401"; !strings.Contains(errmsg, want) {
		t.Errorf("parseErrorResponse() error = %v, want err message %v", err, want)
	}
	if want := http.StatusText(http.StatusUnauthorized); !strings.Contains(errmsg, want) {
		t.Errorf("parseErrorResponse() error = %v, want err message %v", err, want)
	}
}
