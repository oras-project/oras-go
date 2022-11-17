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

// mergeStatus represents the status of a merge operation.
type mergeStatus struct {
	// main indicates if the item is a main item
	main bool
	// err is the error of the main item
	err error
}

// Merge represents a merge operation.
// The state transfer is shown as below:
//
//	           +---------+
//	           |  Start  +-------+-----------+
//	           +----+----+       |           |
//	                |            |           |
//	                v            v           v
//	           +----+----+   +---+---+   +---+---+
//	   +-------+ Prepare +<--+Pending+-->+Waiting|
//	   |       +----+----+   +-------+   +---+---+
//	   |            |                        |
//	   |            v                        |
//	   +        +---+----+                   |
//	On Error    |Resolve |                   |
//	   +        +---+----+                   |
//	   |            |                        |
//	   |            v                        |
//	   |        +---+----+                   |
//	   +------->+Complete+<------------------+
//	            +---+----+
//	                |
//	                v
//	            +---+----+
//	            |  End   |
//	            +--------+
type Merge[T any] struct {
	lock          sync.Mutex
	committed     bool
	items         []T
	status        chan mergeStatus
	pending       []T
	pendingStatus chan mergeStatus
}

// Do calls prepare before the merge and calls resolve on the merged items.
// If Do is called multiple times, only one of the calls will be selected to
// invoke prepare and resolve.
func (m *Merge[T]) Do(item T, prepare func() error, resolve func(items []T) error) error {
	status := <-m.assign(item)
	if status.main {
		err := prepare()
		items := m.commit()
		if err == nil {
			err = resolve(items)
		}
		m.complete(err)
		return err
	}
	return status.err
}

// assign adds a new item into the merge list.
func (m *Merge[T]) assign(item T) <-chan mergeStatus {
	m.lock.Lock()
	defer m.lock.Unlock()

	if m.committed {
		if m.pendingStatus == nil {
			m.pendingStatus = make(chan mergeStatus, 1)
		}
		m.pending = append(m.pending, item)
		return m.pendingStatus
	}

	if m.status == nil {
		m.status = make(chan mergeStatus, 1)
		m.status <- mergeStatus{main: true}
	}
	m.items = append(m.items, item)
	return m.status
}

// commit commits the merge, and the merge is then ready for resolve.
func (m *Merge[T]) commit() []T {
	m.lock.Lock()
	defer m.lock.Unlock()

	m.committed = true
	return m.items
}

// complete completes the previous merge, and moves the pending items to the
// stage for the next merge.
func (m *Merge[T]) complete(err error) {
	// notify results
	if err == nil {
		close(m.status)
	} else {
		remaining := len(m.items) - 1
		status := m.status
		for remaining > 0 {
			status <- mergeStatus{err: err}
			remaining--
		}
	}

	// move pending items to the stage
	m.lock.Lock()
	defer m.lock.Unlock()

	m.committed = false
	m.items = m.pending
	m.status = m.pendingStatus
	m.pending = nil
	m.pendingStatus = nil

	if m.status != nil {
		m.status <- mergeStatus{main: true}
	}
}
