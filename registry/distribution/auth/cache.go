package auth

import (
	"context"

	"oras.land/oras-go/v2/errdef"
)

var DefaultCache Cache

type Cache interface {
	GetScheme(ctx context.Context, registry string) (string, error)
	GetToken(ctx context.Context, registry, key string) (string, error)
	Set(ctx context.Context, registry, scheme, key string, fetch func(context.Context) (string, error)) (string, error)
}

type noCache struct{}

func (noCache) GetScheme(ctx context.Context, registry string) (string, error) {
	return "", errdef.ErrNotFound
}

func (noCache) GetToken(ctx context.Context, registry string, key string) (string, error) {
	return "", errdef.ErrNotFound
}

func (noCache) Set(ctx context.Context, registry string, scheme string, key string, fetch func(context.Context) (string, error)) (string, error) {
	return fetch(ctx)
}
