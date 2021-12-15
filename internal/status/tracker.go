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
package status

import (
	"sync"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/internal/descriptor"
)

// Tracker tracks status described by a key.
type Tracker sync.Map // map[interface{}]chan struct{}

// NewTracker creates a new status tracker.
func NewTracker() *Tracker {
	return &Tracker{}
}

// TryCommit tries to commit the work item.
// Returns true if committed. A channel is also returned for sending
// notifications. Once the work is done, the channel should be closed.
// Returns false if the work is done or still in progress.
func (t *Tracker) TryCommit(item interface{}) (chan struct{}, bool) {
	status, exists := (*sync.Map)(t).LoadOrStore(item, make(chan struct{}))
	return status.(chan struct{}), !exists
}

// DoneAndDelete removes the work item, and sends done notification to
// receivers, if any.
// Return true if the work item exists and is marked as done.
// Return false if the work item is not found.
func (t *Tracker) DoneAndDelete(item interface{}) bool {
	status, exists := (*sync.Map)(t).LoadAndDelete(item)
	if exists {
		close(status.(chan struct{}))
	}
	return exists
}

// DescriptorTracker tracks content status described by a descriptor.
type DescriptorTracker Tracker

// NewDescriptorTracker creates a new content status tracker.
func NewDescriptorTracker() *DescriptorTracker {
	return &DescriptorTracker{}
}

// TryCommit tries to commit the work for the target descriptor.
// Returns true if committed. A channel is also returned for sending
// notifications. Once the work is done, the channel should be closed.
// Returns false if the work is done or still in progress.
func (t *DescriptorTracker) TryCommit(target ocispec.Descriptor) (chan struct{}, bool) {
	item := descriptor.FromOCI(target)
	return (*Tracker)(t).TryCommit(item)
}
