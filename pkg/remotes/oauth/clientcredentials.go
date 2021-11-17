package oauth

import (
	"context"
	"fmt"
	"net/http"

	"golang.org/x/oauth2/clientcredentials"
	"oras.land/oras-go/pkg/remotes"
)

// NewRegistryWithClientCredentials will generate an authenticated oauth2 client running the 2-step oauth flow, the client will auto-update
func NewClientCredentialsAccess(oauth clientcredentials.Config) remotes.Access {
	return &clientCredentialsAccess{
		config: oauth,
	}
}

type clientCredentialsAccess struct {
	config clientcredentials.Config
}

func (a *clientCredentialsAccess) GetClient(ctx context.Context) (*http.Client, error) {
	c := a.config.Client(ctx)
	if c == nil {
		return nil, fmt.Errorf("could not create client")
	}

	return c, nil
}
