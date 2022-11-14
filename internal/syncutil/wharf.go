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

// Wharf is a wharf with ferries commanded by an elected gopher.
type Wharf struct {
	gate            sync.Mutex
	closed          bool
	ferry           []any
	ferryCaptain    chan bool
	platform        []any
	platformCaptain chan bool
}

// NewWharf creates a virtual wharf with ferries.
func NewWharf() *Wharf {
	return &Wharf{}
}

// Enter enters the wharf, holding a ticket.
// A channel is returned to indicate if the current gopher is elected as a
// captain.
// If a gopher is elected as a caption, it is responsible to Close() the gate
// and set sail to process the tickets of gophers on boarded, and Arrive() once
// all tickets are checked.
// Otherwise, a gopher is known to be a passenger and it can check its ticket.
func (w *Wharf) Enter(ticket any) <-chan bool {
	w.gate.Lock()
	defer w.gate.Unlock()

	if w.closed {
		if w.platformCaptain == nil {
			w.platformCaptain = make(chan bool, 1)
		}
		w.platform = append(w.platform, ticket)
		return w.platformCaptain
	}

	if w.ferryCaptain == nil {
		w.ferryCaptain = make(chan bool, 1)
		w.ferryCaptain <- true
	}
	w.ferry = append(w.ferry, ticket)
	return w.ferryCaptain
}

// Close closes the gate for onboarding, returning the tickets of all on boarded
// gophers.
func (w *Wharf) Close() []any {
	w.gate.Lock()
	defer w.gate.Unlock()

	w.closed = true
	return w.ferry
}

// Arrive notifies all passengers that the ferry has arrived its destination.
// Onboarding gate is now open.
func (w *Wharf) Arrive() {
	close(w.ferryCaptain)

	w.gate.Lock()
	defer w.gate.Unlock()

	w.closed = false
	w.ferry = w.platform
	w.ferryCaptain = w.platformCaptain
	w.platform = nil
	w.platformCaptain = nil

	if w.ferryCaptain != nil {
		w.ferryCaptain <- true
	}
}

// idle returns true if the wharf is not actively used.
func (w *Wharf) idle() bool {
	w.gate.Lock()
	defer w.gate.Unlock()

	return (w.closed && w.platform == nil) || (!w.closed && w.ferry == nil)
}
