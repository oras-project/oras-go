package remotes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"golang.org/x/oauth2"
)

func NewRegistryWithBasicAuthorization(ctx context.Context, reference, username, password string, scopes string) *Registry {
	host, ns, ref, err := validate(reference)
	if err != nil {
		return nil
	}

	// Will be used by the token source when retrieving new tokens, this is different then the client below this line
	ctx = context.WithValue(ctx, oauth2.HTTPClient, newHttpClient())

	client := oauth2.NewClient(ctx, newBasicAuthTokenSource(ctx, host, username, password, scopes))
	if client == nil {
		return nil
	}

	// By default golang forwards all headers when it redirects
	// If the url lives under the same subdomain, domain, it will also forward the Auth header
	// I haven't investigated, but it's possible after the header pruning, it gets added back by oauth2 since oauth2 owns the transport
	// To fix this check if the hosts match, and that we aren't deleting a legit Authorization header, for example maybe the next redirect
	// could somehow authenticate somewhere in between. So make sure the header being deleted is the auth header from the previous request
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > 0 && req.URL.Host != via[0].Host && req.Header.Get("Authorization") == via[0].Header.Get("Authorization") {
			req.Header.Del("Authorization") // if it doesn't exist this is a no-op
			return nil
		}
		return nil
	}

	registry := &Registry{
		client:    client,
		host:      host,
		namespace: ns,
		ref:       ref,
	}

	return registry
}

type basicAuthTokenSource struct {
	tokenFunc TokenFunc
}

func newBasicAuthTokenSource(ctx context.Context, namespace, username, password string, scopes string) oauth2.TokenSource {
	src := &basicAuthTokenSource{
		tokenFunc: func() (*oauth2.Token, error) {
			req, err := http.NewRequest("GET", fmt.Sprintf("https://%s/oauth2/token?service=%s&scope=%s", namespace, namespace, scopes), nil)
			if err != nil {
				return nil, err
			}
			req.SetBasicAuth(username, password)

			c, ok := ctx.Value(oauth2.HTTPClient).(*http.Client)
			if !ok {
				c = http.DefaultClient
			}

			resp, err := c.Do(req)
			if err != nil {
				return nil, err
			}

			defer resp.Body.Close()

			if resp.StatusCode != 200 {
				return nil, fmt.Errorf("could not get access token")
			}

			token := &oauth2.Token{}
			if err := json.NewDecoder(resp.Body).Decode(token); err != nil {
				return nil, err
			}

			return token, nil
		},
	}

	token, err := src.Token()
	if err != nil {
		return nil
	}

	return oauth2.ReuseTokenSource(token, src)
}

type TokenFunc = func() (*oauth2.Token, error)

func (b basicAuthTokenSource) Token() (*oauth2.Token, error) {
	return b.tokenFunc()
}
