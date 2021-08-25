package oras

import (
	"context"

	"golang.org/x/oauth2"
)

func NewRegistryWithBasicAuthorization(ctx context.Context, namespace, username, password string, scopes ...string) *Registry {
	client := oauth2.NewClient(ctx, newBasicAuthTokenSource(ctx, username, password, scopes))
	if client == nil {
		return nil
	}

	r := &Registry{
		client:    client,
		namespace: namespace,
	}

	if validateNamespace(namespace) {
		return r
	}

	return nil
}

type basicAuthTokenSource struct {
	tokenFunc TokenFunc
}

func newBasicAuthTokenSource(ctx context.Context, username, password string, scopes []string) oauth2.TokenSource {
	src := &basicAuthTokenSource{
		tokenFunc: func() (*oauth2.Token, error) {
			config := oauth2.Config{}
			return config.PasswordCredentialsToken(ctx, username, password)
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
