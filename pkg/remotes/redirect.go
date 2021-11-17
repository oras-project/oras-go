package remotes

import (
	"net/http"
	"regexp"
	"time"
)

// NewRedirectError is a function that takes a redirected request and wraps it into an error
// This error is handled by the registry
func NewRedirectError(req *http.Request) *RedirectError {
	return &RedirectError{
		retry:    req,
		isSasURL: sasURLFormat.MatchString(req.URL.String()),
	}
}

// RedirectError is an opaque type returned when encountering a 302 Redirect
type RedirectError struct {
	retry    *http.Request
	isSasURL bool
	error
}

// Retry is a function that will execute the redirected request
// if the redirected request is a SAS uri, then a fresh client will be used instead of the passed in client
func (r RedirectError) Retry(client *http.Client) (*http.Response, error) {
	if r.isSasURL {
		// Create a fresh client
		client = &http.Client{
			Timeout: time.Second * 10, // With a SAS Uri there should be no back and forth
		}
		return client.Do(r.retry)
	}

	return client.Do(r.retry)
}

// SAS stands for Shared Access Signature, which is a URI that can grant limited temporary access to resources
// Generally, the uri is all that is required to gain access
var sasURLFormat = regexp.MustCompile(`[?]se=\d\d\d\d-\d\d-\d\d.*&sig=[\w%\d]+&sp=\w+&spr=\w+&sr=\w+&sv=\d\d\d\d-\d\d-\d\d`)
