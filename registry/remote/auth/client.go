package auth

import "net/http"

// DefaultClient is the default auth-decorated client.
var DefaultClient = &Authorizer{
	Header: http.Header{
		"User-Agent": {"oras-go"},
	},
	Cache: DefaultCache,
}

// Client is an auth-decorated HTTP client.
type Client interface {
	// Do sends an HTTP request and returns an HTTP response with authentication
	// resolved.
	//
	// Unlike http.RoundTripper, Client can attempt to interpret the response
	// and handle higher-level protocol details such as redirects and
	// authentication.
	//
	// Like http.RoundTripper, Client should not modify the request, and must
	// always close the request body.
	Do(*http.Request) (*http.Response, error)
}
