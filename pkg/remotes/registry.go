package remotes

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// NewRegistry is a function to return an instance of a Registry struct
// The Registry struct can be adapted to several platforms and provides
// the rest api client to the registry
func NewRegistry(host, ns string, provider AccessProvider) *Registry {
	return &Registry{
		provider:  provider,
		host:      host,
		namespace: ns,
	}
}

type (
	// Client is an interface to communicate with the registry
	Client interface {
		Respository() (host, namespace string)
		// Do is a function that sends the request and handles the response
		Do(ctx context.Context, req *http.Request) (*http.Response, error)
	}

	// Registry is an opaqueish type which represents an OCI V2 API registry
	Registry struct {
		host      string
		namespace string
		provider  AccessProvider
		*http.Client
	}
)

func (r *Registry) Respository() (string, string) {
	return r.host, r.namespace
}

// Do is a function that does the request, error handling is concentrated in this method
func (r *Registry) Do(ctx context.Context, req *http.Request) (*http.Response, error) {
	if r.Client == nil {
		r.setClient(http.DefaultClient) // TODO make a default anonymous client (tune to fail fast since most things need to be authenticated)
	}

	resp, err := r.do(req)
	if err != nil {
		// This comes from the redirect handler
		ne, ok := err.(*url.Error)
		if ok {
			re, ok := ne.Err.(*RedirectError)
			if ok {
				resp, err = re.Retry(r.Client)
				if err != nil {
					resp.Body.Close()
					return nil, err
				}

				return resp, nil
			}
		}

		if errors.Is(err, ErrAuthChallenge) {
			challengeError, ok := err.(*AuthChallengeError)
			if ok {
				// Check our provider for access
				access, err := r.provider.GetAccess(ctx, challengeError)
				if err != nil {
					if resp != nil && resp.Request != nil {
						defer resp.Body.Close()
					}

					return nil, err
				}

				// Get a new client once we have access
				c, err := access.GetClient(ctx)
				if err != nil {
					if resp != nil && resp.Request != nil {
						defer resp.Body.Close()
					}
					return nil, err
				}
				r.setClient(c)

				resp, err = c.Do(req)
				if err != nil {
					return nil, err
				}

				return resp, nil
			}
		}

		return nil, err
	}

	return resp, nil
}

// do calls the concrete http client, and handles http related status code issues
func (r *Registry) do(req *http.Request) (*http.Response, error) {
	resp, err := r.Client.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode > 299 {
		if resp.StatusCode == 401 {
			c, ok := resp.Header["Www-Authenticate"]
			if ok {
				resp.Body.Close()
				// TODO not sure what the delimitter would be
				return nil, NewAuthChallengeError(strings.Join(c, ","))
			}
			resp.Body.Close()
			return nil, fmt.Errorf("not authenticated")
		}

		defer resp.Body.Close()

		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	return resp, nil
}

// setClient is a function that sets the current http.Client, it also ensures that the CheckRedirect callback
// returns a redirect error for processing
func (r *Registry) setClient(client *http.Client) {
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > 0 && req.URL.Host != via[0].Host &&
			req.Header.Get("Authorization") == via[0].Header.Get("Authorization") {
			return NewRedirectError(req)
		}
		return nil
	}
	r.Client = client
}
