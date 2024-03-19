package syncutil

import (
	"sync"
	"sync/atomic"
)

// OnceRetryOnError returns a function that invokes f only once if f returns
// nil. Otherwise, it retries on the next call.
func OnceRetryOnError(f func() error) func() error {
	var done atomic.Bool
	var lock sync.Mutex

	return func() error {
		// fast path
		if done.Load() {
			return nil
		}

		// slow path
		lock.Lock()
		defer lock.Unlock()

		if done.Load() {
			return nil
		}
		if err := f(); err != nil {
			return err
		}
		done.Store(true)
		return nil
	}
}
