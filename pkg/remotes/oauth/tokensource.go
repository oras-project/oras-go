package oauth

import (
	"context"
	"fmt"
	"net/http"

	"golang.org/x/oauth2"
	"oras.land/oras-go/pkg/remotes"
)

func NewTokenSourceAccess(tokensource oauth2.TokenSource) remotes.Access {
	return &tokenSourceAccess{tokensource: tokensource}
}

type tokenSourceAccess struct {
	tokensource oauth2.TokenSource
}

func (a *tokenSourceAccess) GetClient(ctx context.Context) (*http.Client, error) {
	client := oauth2.NewClient(ctx, a.tokensource)
	if client == nil {
		return nil, fmt.Errorf("could not create a new client")
	}

	return client, nil
}
