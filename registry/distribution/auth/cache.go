package auth

import (
	"context"
	"fmt"

	"oras.land/oras-go/v2/errdef"
)

var DefaultCache Cache = NewCache()

type Cache interface {
	GetScheme(ctx context.Context, registry string) (string, error)
	GetToken(ctx context.Context, registry, scheme, key string) (string, error)
	Set(ctx context.Context, registry, scheme, key string, fetch func(context.Context) (string, error)) (string, error)
}

type basicCache string

type tokenCache map[string]string

type concurrentCache map[string]interface{}

func NewCache() Cache {
	return &concurrentCache{}
}

func (cc *concurrentCache) GetScheme(ctx context.Context, registry string) (string, error) {
	value, ok := (*cc)[registry]
	if !ok {
		return "", errdef.ErrNotFound
	}
	switch value.(type) {
	case *basicCache:
		return SchemeBasic, nil
	case *tokenCache:
		return SchemeBearer, nil
	}
	return "", errdef.ErrNotFound
}

func (cc *concurrentCache) GetToken(ctx context.Context, registry, scheme, key string) (string, error) {
	value, ok := (*cc)[registry]
	if !ok {
		return "", errdef.ErrNotFound
	}
	switch c := value.(type) {
	case *basicCache:
		return string(*c), nil
	case *tokenCache:
		token, ok := (*c)[key]
		if ok {
			return token, nil
		}
	}
	return "", errdef.ErrNotFound
}

func (cc *concurrentCache) Set(ctx context.Context, registry, scheme, key string, fetch func(context.Context) (string, error)) (string, error) {
	switch scheme {
	case SchemeBasic:
		token, err := fetch(ctx)
		if err != nil {
			return "", err
		}
		(*cc)[registry] = token
		return token, nil
	case SchemeBearer:
		token, err := fetch(ctx)
		if err != nil {
			return "", err
		}
		scopes, ok := (*cc)[registry].(*tokenCache)
		if !ok {
			scopes = &tokenCache{}
			(*cc)[registry] = scopes
		}
		(*scopes)[key] = token
		return token, nil
	}

	return "", fmt.Errorf("unknown scheme: %s", scheme)
}

type noCache struct{}

func (noCache) GetScheme(ctx context.Context, registry string) (string, error) {
	return "", errdef.ErrNotFound
}

func (noCache) GetToken(ctx context.Context, registry, scheme, key string) (string, error) {
	return "", errdef.ErrNotFound
}

func (noCache) Set(ctx context.Context, registry, scheme, key string, fetch func(context.Context) (string, error)) (string, error) {
	return fetch(ctx)
}
