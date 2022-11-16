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

// sailStatus represents if a gopher is elected as captain or not, and if the
// gopher arrives in one piece.
type sailStatus struct {
	Elected bool
	Error   error
}

// Wharf is a wharf with ferries commanded by an elected gopher.
type Wharf[T any] struct {
	gate           sync.Mutex
	closed         bool
	ferry          []T
	ferryStatus    chan sailStatus
	platform       []T
	platformStatus chan sailStatus
}

// NewWharf creates a virtual wharf with ferries.
func NewWharf[T any]() *Wharf[T] {
	return &Wharf[T]{}
}

// Travel travels to the destination. If necessary, one gopher needs to learn
// how to sail.
func (w *Wharf[T]) Travel(ticket T, learn func() error, sail func(tickets []T) error) error {
	status := <-w.enter(ticket)
	if status.Elected {
		if err := learn(); err != nil {
			w.resign()
			return err
		}
		tickets := w.close()
		err := sail(tickets)
		w.arrive(err)
		return err
	}
	return status.Error
}

// enter enters the wharf, holding a ticket.
// A channel is returned to indicate if the current gopher is elected as a
// captain.
// If a gopher is elected as a captain, it is responsible to Close() the gate
// and set sail to process the tickets of gophers on boarded, and Arrive() once
// all tickets are checked.
// Otherwise, a gopher is known to be a passenger and it can check its ticket.
func (w *Wharf[T]) enter(ticket T) <-chan sailStatus {
	w.gate.Lock()
	defer w.gate.Unlock()

	if w.closed {
		if w.platformStatus == nil {
			w.platformStatus = make(chan sailStatus, 1)
		}
		w.platform = append(w.platform, ticket)
		return w.platformStatus
	}

	if w.ferryStatus == nil {
		w.ferryStatus = make(chan sailStatus, 1)
		w.ferryStatus <- sailStatus{Elected: true}
	}
	w.ferry = append(w.ferry, ticket)
	return w.ferryStatus
}

// resign notifies all passengers that the captain resigns. A captain can resign
// at the wharf but will live with the ferry. If captain resign in the middle of
// the journey, the ferry sinks.
func (w *Wharf[T]) resign() {
	w.gate.Lock()
	defer w.gate.Unlock()

	if len(w.ferry) > 1 {
		w.ferry = w.ferry[1:]
		w.ferryStatus <- sailStatus{Elected: true}
		return
	}

	close(w.ferryStatus)
	w.ferry = nil
	w.ferryStatus = nil
}

// close closes the gate for onboarding, returning the tickets of all on boarded
// gophers.
func (w *Wharf[T]) close() []T {
	w.gate.Lock()
	defer w.gate.Unlock()

	w.closed = true
	return w.ferry
}

// arrive notifies all passengers that the ferry has arrived its destination, or
// sunk due to error.
// Onboarding gate is now open.
func (w *Wharf[T]) arrive(err error) {
	// every one lives if arrived
	if err == nil {
		close(w.ferryStatus)
	}

	w.gate.Lock()
	defer w.gate.Unlock()

	w.closed = false
	remaining := len(w.ferry) - 1
	status := w.ferryStatus
	w.ferry = w.platform
	w.ferryStatus = w.platformStatus
	w.platform = nil
	w.platformStatus = nil

	if w.ferryStatus != nil {
		w.ferryStatus <- sailStatus{Elected: true}
	}

	// deliver difficult message if sank
	if err != nil {
		for remaining > 0 {
			status <- sailStatus{Error: err}
			remaining--
		}
	}
}

// idle returns true if the wharf is not actively used.
func (w *Wharf[T]) idle() bool {
	w.gate.Lock()
	defer w.gate.Unlock()

	return (w.closed && w.platform == nil) || (!w.closed && w.ferry == nil)
}
