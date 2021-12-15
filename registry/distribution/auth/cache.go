package auth

import (
	"context"
	"fmt"
	"sync"

	"oras.land/oras-go/v2/errdef"
)

var DefaultCache Cache = NewCache()

type Cache interface {
	GetScheme(ctx context.Context, registry string) (string, error)
	GetToken(ctx context.Context, registry, scheme, key string) (string, error)
	Set(ctx context.Context, registry, scheme, key string, fetch func(context.Context) (string, error)) (string, error)
}

type basicCache string

type tokenCache struct {
	lock   sync.RWMutex
	tokens map[string]string
}

type concurrentCache struct {
	lock  sync.RWMutex
	cache map[string]interface{}
}

func NewCache() Cache {
	return &concurrentCache{
		cache: make(map[string]interface{}),
	}
}

func (cc *concurrentCache) GetScheme(ctx context.Context, registry string) (string, error) {
	cc.lock.RLock()
	value, ok := cc.cache[registry]
	cc.lock.RUnlock()
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
	cc.lock.RLock()
	value, ok := cc.cache[registry]
	cc.lock.RUnlock()
	if !ok {
		return "", errdef.ErrNotFound
	}
	switch c := value.(type) {
	case *basicCache:
		return string(*c), nil
	case *tokenCache:
		c.lock.RLock()
		token, ok := c.tokens[key]
		c.lock.RUnlock()
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
		cc.lock.Lock()
		cc.cache[registry] = token
		cc.lock.Unlock()
		return token, nil
	case SchemeBearer:
		token, err := fetch(ctx)
		if err != nil {
			return "", err
		}
		cc.lock.Lock()
		scopes, ok := cc.cache[registry].(*tokenCache)
		if !ok {
			scopes = &tokenCache{
				tokens: make(map[string]string),
			}
			cc.cache[registry] = scopes
		}
		cc.lock.Unlock()
		scopes.lock.Lock()
		scopes.tokens[key] = token
		scopes.lock.Unlock()
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
