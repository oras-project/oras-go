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
package lockutil

import (
	"sync"
)

// ReferenceLocker represents a pool of locks identified by a reference.
type ReferenceLocker struct {
	locks sync.Map //map[string]*sync.Mutex
}

// New creates a ReferenceLocker.
func New() *ReferenceLocker {
	return &ReferenceLocker{}
}

// Close closes the ReferenceLocker.
func (rl *ReferenceLocker) Close() {
	rl.locks.Range(func(key interface{}, value interface{}) bool {
		rl.locks.Delete(key)
		return true
	})
}

// Lock locks a reference string
func (rl *ReferenceLocker) Lock(ref string) {
	val, _ := rl.locks.LoadOrStore(ref, &sync.Mutex{})
	lock := val.(*sync.Mutex)
	lock.Lock()
}

// UnLock unlocks a reference string.
func (rl *ReferenceLocker) Unlock(ref string) {
	val, exists := rl.locks.Load(ref)
	if exists {
		lock := val.(*sync.Mutex)
		lock.Unlock()
	}
}
