package auth

import (
	"context"
	"fmt"
	"sync"

	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/internal/syncutil"
)

var DefaultCache Cache = NewCache()

type Cache interface {
	GetScheme(ctx context.Context, registry string) (Scheme, error)
	GetToken(ctx context.Context, registry string, scheme Scheme, key string) (string, error)
	Set(ctx context.Context, registry string, scheme Scheme, key string, fetch func(context.Context) (string, error)) (string, error)
}

type basicCache string

type tokenCache sync.Map // map[string]string

type concurrentCache struct {
	status    sync.Map // map[string]fetchOnceFunc
	cacheLock sync.RWMutex
	cache     map[string]interface{}
}

func NewCache() Cache {
	return &concurrentCache{
		cache: make(map[string]interface{}),
	}
}

func (cc *concurrentCache) GetScheme(ctx context.Context, registry string) (Scheme, error) {
	cc.cacheLock.RLock()
	value, ok := cc.cache[registry]
	cc.cacheLock.RUnlock()
	if !ok {
		return SchemeUnknown, errdef.ErrNotFound
	}
	switch value.(type) {
	case *basicCache:
		return SchemeBasic, nil
	case *tokenCache:
		return SchemeBearer, nil
	}
	return SchemeUnknown, errdef.ErrNotFound
}

func (cc *concurrentCache) GetToken(ctx context.Context, registry string, scheme Scheme, key string) (string, error) {
	cc.cacheLock.RLock()
	value, ok := cc.cache[registry]
	cc.cacheLock.RUnlock()
	if !ok {
		return "", errdef.ErrNotFound
	}
	switch c := value.(type) {
	case *basicCache:
		return *(*string)(c), nil
	case *tokenCache:
		token, ok := (*sync.Map)(c).Load(key)
		if ok {
			return token.(string), nil
		}
	}
	return "", errdef.ErrNotFound
}

func (cc *concurrentCache) Set(ctx context.Context, registry string, scheme Scheme, key string, fetch func(context.Context) (string, error)) (string, error) {
	switch scheme {
	case SchemeBasic, SchemeBearer:
	default:
		return "", fmt.Errorf("unknown scheme: %s", scheme)
	}

	statusKey := scheme.String() + " " + key
	statusValue, _ := cc.status.LoadOrStore(statusKey, syncutil.NewOnce())
	aggregatedFetch := statusValue.(*syncutil.Once)
	fetchedFirst, result, err := aggregatedFetch.Do(ctx, func() (interface{}, error) {
		return fetch(ctx)
	})
	if fetchedFirst {
		cc.status.Delete(statusKey)
	}
	if err != nil {
		return "", err
	}
	token := result.(string)
	if !fetchedFirst {
		return token, nil
	}

	switch scheme {
	case SchemeBasic:
		cc.cacheLock.Lock()
		cc.cache[registry] = token
		cc.cacheLock.Unlock()
	case SchemeBearer:
		cc.cacheLock.Lock()
		scopes, ok := cc.cache[registry].(*tokenCache)
		if !ok {
			scopes = &tokenCache{}
			cc.cache[registry] = scopes
		}
		cc.cacheLock.Unlock()
		(*sync.Map)(scopes).Store(key, token)
	}

	return token, nil
}

type noCache struct{}

func (noCache) GetScheme(ctx context.Context, registry string) (Scheme, error) {
	return SchemeUnknown, errdef.ErrNotFound
}

func (noCache) GetToken(ctx context.Context, registry string, scheme Scheme, key string) (string, error) {
	return "", errdef.ErrNotFound
}

func (noCache) Set(ctx context.Context, registry string, scheme Scheme, key string, fetch func(context.Context) (string, error)) (string, error) {
	return fetch(ctx)
}
