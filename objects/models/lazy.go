/*
Copyright The ORAS Authors.
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package models

import "sync"

// lazy provides thread-safe lazy loading with retry on transient errors.
// Unlike sync.Once, failed loads are NOT cached — subsequent calls to get()
// will retry the load function. Only successful results are cached.
//
// The mutex is NOT held during the load() call to avoid blocking concurrent
// readers during potentially slow I/O (network fetches). This means two
// concurrent first-time callers may both invoke load(), similar to how
// sync.Map's LoadOrStore works. This is acceptable because loads are
// idempotent and the alternative of holding a mutex across I/O is worse.
type lazy[T any] struct {
	mu     sync.Mutex
	val    T
	loaded bool
}

// get returns the cached value if loaded; otherwise calls load() to produce it.
// On success, the result is cached for future calls.
// On error, the result is NOT cached, allowing retry with a different context.
//
// Note: load() is called without holding the mutex, so concurrent callers
// may both call load() on the first miss. The first successful result to
// acquire the lock is stored; subsequent writers find loaded==true and
// return the already-stored value.
func (l *lazy[T]) get(load func() (T, error)) (T, error) {
	// Fast path: already loaded
	l.mu.Lock()
	if l.loaded {
		val := l.val
		l.mu.Unlock()
		return val, nil
	}
	l.mu.Unlock()

	// Slow path: call load without holding the lock
	val, err := load()
	if err != nil {
		var zero T
		return zero, err
	}

	// Store result under lock (another goroutine may have loaded first — that's OK,
	// we accept the last-writer-wins race for correctness, but we avoid double network I/O
	// by checking again before overwriting).
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.loaded {
		l.val = val
		l.loaded = true
	}
	return l.val, nil
}

// set explicitly sets the cached value. This is thread-safe.
func (l *lazy[T]) set(val T) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.val = val
	l.loaded = true
}

// peek returns the cached value without triggering a load.
// Returns (value, true) if loaded, or (zero, false) if not yet loaded.
func (l *lazy[T]) peek() (T, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.loaded {
		return l.val, true
	}
	var zero T
	return zero, false
}
