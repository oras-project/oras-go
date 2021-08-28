package remotes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/oauth2"
)

func NewRegistryWithBasicAuthorization(ctx context.Context, ref, username, password string, scopes string) *Registry {
	host, ns, _, err := validate(ref)
	if err != nil {
		return nil
	}

	// Will be used by the token source when retrieving new tokens, this is different then the client below this line
	ctx = context.WithValue(ctx, oauth2.HTTPClient, newHttpClient())

	client := oauth2.NewClient(ctx, newBasicAuthTokenSource(ctx, host, username, password, scopes))
	if client == nil {
		return nil
	}

	prepareOAuth2Client(client)

	registry := &Registry{
		client:      client,
		host:        host,
		namespace:   ns,
		descriptors: make(map[reference]*ocispec.Descriptor),
		manifest:    make(map[reference]*ocispec.Manifest),
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
