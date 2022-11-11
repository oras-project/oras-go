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

package syncutil

import "sync"

// Quay is a quay with scalable wharves.
type Quay struct {
	gate    sync.Mutex
	wharves map[any]*Wharf
}

// NewQuay creates a virtual scalable quay.
func NewQuay() *Quay {
	return &Quay{}
}

// Enter enters a specific wharf, holding a ticket.
// A captain gopher is responsible to dispose the wharf if it is no longer
// needed, using the returned function.
func (q *Quay) Enter(wharfID, ticket any) (*Wharf, <-chan bool, func()) {
	q.gate.Lock()
	defer q.gate.Unlock()

	wharf, ok := q.wharves[wharfID]
	if !ok {
		wharf = NewWharf()
		if q.wharves == nil {
			q.wharves = map[any]*Wharf{
				wharfID: wharf,
			}
		} else {
			q.wharves[wharfID] = wharf
		}
	}

	return wharf, wharf.Enter(ticket), func() {
		q.gate.Lock()
		defer q.gate.Unlock()
		if wharf.idle() {
			delete(q.wharves, wharfID)
		}
	}
}
