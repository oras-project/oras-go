package remotes

import (
	"context"

	"golang.org/x/oauth2/clientcredentials"
)

// NewRegistryWithClientCredentials will generate an authenticated oauth2 client running the 2-step oauth flow, the client will auto-update
func NewRegistryWithClientCredentials(ctx context.Context, namespace string, oauth clientcredentials.Config) *Registry {
	// TODO set this later after some research on http client option defaults
	// ctx = context.WithValue(ctx, oauth2.HTTPClient, newHttpClient())

	registry := &Registry{
		client:    oauth.Client(ctx),
		namespace: namespace,
	}

	namespace, err := validateNamespace(namespace)
	if err != nil {
		return registry
	}

	resp, err := registry.client.Get("v2/")
	if err != nil {
		return nil
	}

	defer resp.Body.Close()

	return registry
}
