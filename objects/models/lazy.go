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

// lazyCall holds the result of an in-flight load and a channel that is
// closed when the load completes (successfully or not).
type lazyCall[T any] struct {
	done chan struct{}
	val  T
	err  error
}

// lazy provides thread-safe lazy loading with exactly-once semantics on
// success and retry on transient errors.
//
// Unlike sync.Once, failed loads are NOT cached — subsequent calls to get()
// will retry the load function. Only successful results are cached.
//
// Concurrent calls while a load is in flight block on a channel rather than
// triggering duplicate loads (singleflight semantics).
type lazy[T any] struct {
	mu       sync.Mutex
	val      T
	loaded   bool
	inflight *lazyCall[T]
}

// get returns the cached value if loaded; otherwise calls load() exactly once.
// Concurrent callers block until the in-flight load completes.
// On success, the result is cached for all future calls.
// On error, nothing is cached — the next call will retry.
func (l *lazy[T]) get(load func() (T, error)) (T, error) {
	l.mu.Lock()
	// Fast path: already loaded.
	if l.loaded {
		val := l.val
		l.mu.Unlock()
		return val, nil
	}
	// In-flight: join the existing load and wait.
	if l.inflight != nil {
		call := l.inflight
		l.mu.Unlock()
		<-call.done
		return call.val, call.err
	}
	// Slow path: this goroutine owns the load.
	call := &lazyCall[T]{done: make(chan struct{})}
	l.inflight = call
	l.mu.Unlock()

	call.val, call.err = load()

	l.mu.Lock()
	if call.err == nil {
		l.val = call.val
		l.loaded = true
	}
	l.inflight = nil
	l.mu.Unlock()

	close(call.done) // wake all waiters
	return call.val, call.err
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
