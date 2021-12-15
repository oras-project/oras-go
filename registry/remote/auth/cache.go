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

func (cc *concurrentCache) GetScheme(ctx context.Context, registry string) (string, error) {
	cc.cacheLock.RLock()
	value, ok := cc.cache[registry]
	cc.cacheLock.RUnlock()
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

func (cc *concurrentCache) Set(ctx context.Context, registry, scheme, key string, fetch func(context.Context) (string, error)) (string, error) {
	switch scheme {
	case SchemeBasic, SchemeBearer:
	default:
		return "", fmt.Errorf("unknown scheme: %s", scheme)
	}

	statusKey := scheme + " " + key
	statusValue, _ := cc.status.LoadOrStore(statusKey, newFetchOnceFunc())
	fetchOnce := statusValue.(fetchOnceFunc)
	primary, token, err := fetchOnce(ctx, fetch)
	if primary {
		cc.status.Delete(statusKey)
	}
	if err != nil {
		return "", err
	}
	if !primary {
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

type fetchOnceFunc func(context.Context, func(context.Context) (string, error)) (bool, string, error)

func newFetchOnceFunc() fetchOnceFunc {
	var token string
	var err error
	once := make(chan bool, 1)
	once <- true
	return func(ctx context.Context, fetch func(context.Context) (string, error)) (bool, string, error) {
		for {
			select {
			case notDone := <-once:
				if !notDone {
					return false, token, err
				}
				token, err = fetch(ctx)
				if err == context.Canceled || err == context.DeadlineExceeded {
					once <- true
					return false, "", err
				}
				close(once)
				return true, token, err
			case <-ctx.Done():
				return false, "", ctx.Err()
			}
		}
	}
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
