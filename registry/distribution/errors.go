package distribution

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"unicode"
)

// requestError contains a single error.
type requestError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Error returns a error string describing the error.
func (e requestError) Error() string {
	code := strings.Map(func(r rune) rune {
		if r == '_' {
			return ' '
		}
		return unicode.ToLower(r)
	}, e.Code)
	if e.Message == "" {
		return code
	}
	return fmt.Sprintf("%s: %s", code, e.Message)
}

// requestErrors is a bundle of requestError.
type requestErrors []requestError

// Error returns a error string describing the error.
func (errs requestErrors) Error() string {
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

// parseErrorResponse parses the error returned by the remote registry.
func parseErrorResponse(resp *http.Response) error {
	var errmsg string
	var body struct {
		Errors requestErrors `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err == nil && len(body.Errors) > 0 {
		errmsg = body.Errors.Error()
	} else {
		errmsg = http.StatusText(resp.StatusCode)
	}
	return fmt.Errorf("%s %q: unexpected status code %d: %v", resp.Request.Method, resp.Request.URL, resp.StatusCode, errmsg)
}
