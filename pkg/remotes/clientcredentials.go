package remotes

import (
	"context"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

// NewRegistryWithClientCredentials will generate an authenticated oauth2 client running the 2-step oauth flow, the client will auto-update
func NewRegistryWithClientCredentials(ctx context.Context, reference string, oauth clientcredentials.Config) *Registry {
	host, ns, ref, err := validate(reference)
	if err != nil {
		return nil
	}

	// Will be used by the token source when retrieving new tokens, this is different then the client below this line
	ctx = context.WithValue(ctx, oauth2.HTTPClient, newHttpClient())

	c := oauth.Client(ctx)
	prepareOAuth2Client(c)

	registry := &Registry{
		client:    c,
		namespace: ns,
		host:      host,
		ref:       ref,
	}

	return registry
}
