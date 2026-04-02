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

package remote

import (
	"sync/atomic"

	"github.com/oras-project/oras-go/v3/internal/syncutil"
)

// ReferrerCapability tracks a registry's Referrers API support.
// It provides thread-safe state management for the referrers capability.
type ReferrerCapability struct {
	state int32
}

// NewReferrerCapability creates a new ReferrerCapability with unknown state.
func NewReferrerCapability() *ReferrerCapability {
	return &ReferrerCapability{
		state: referrersStateUnknown,
	}
}

// IsSupported returns true if the registry is known to support the Referrers API.
func (c *ReferrerCapability) IsSupported() bool {
	return atomic.LoadInt32(&c.state) == referrersStateSupported
}

// IsUnsupported returns true if the registry is known to NOT support the Referrers API.
func (c *ReferrerCapability) IsUnsupported() bool {
	return atomic.LoadInt32(&c.state) == referrersStateUnsupported
}

// IsUnknown returns true if the registry's Referrers API support is unknown.
func (c *ReferrerCapability) IsUnknown() bool {
	return atomic.LoadInt32(&c.state) == referrersStateUnknown
}

// State returns the current state of the referrers capability.
func (c *ReferrerCapability) State() referrersState {
	return atomic.LoadInt32(&c.state)
}

// SetSupported marks the registry as supporting the Referrers API.
// Returns ErrReferrersCapabilityAlreadySet if the state was already set
// to a different value.
func (c *ReferrerCapability) SetSupported() error {
	return c.setState(referrersStateSupported)
}

// SetUnsupported marks the registry as NOT supporting the Referrers API.
// Returns ErrReferrersCapabilityAlreadySet if the state was already set
// to a different value.
func (c *ReferrerCapability) SetUnsupported() error {
	return c.setState(referrersStateUnsupported)
}

// TrySetSupported attempts to mark the registry as supporting the Referrers API.
// Returns true if the state was successfully set, false if it was already set.
// Unlike SetSupported, this method does not return an error if the state
// matches the current value.
func (c *ReferrerCapability) TrySetSupported() bool {
	return atomic.CompareAndSwapInt32(&c.state, referrersStateUnknown, referrersStateSupported) ||
		c.IsSupported()
}

// TrySetUnsupported attempts to mark the registry as NOT supporting the Referrers API.
// Returns true if the state was successfully set, false if it was already set.
// Unlike SetUnsupported, this method does not return an error if the state
// matches the current value.
func (c *ReferrerCapability) TrySetUnsupported() bool {
	return atomic.CompareAndSwapInt32(&c.state, referrersStateUnknown, referrersStateUnsupported) ||
		c.IsUnsupported()
}

// Reset resets the capability state to unknown.
// This is primarily useful for testing.
func (c *ReferrerCapability) Reset() {
	atomic.StoreInt32(&c.state, referrersStateUnknown)
}

// setState atomically sets the state, returning an error if the state
// was already set to a different value.
func (c *ReferrerCapability) setState(newState referrersState) error {
	if swapped := atomic.CompareAndSwapInt32(&c.state, referrersStateUnknown, newState); swapped {
		return nil
	}
	// Check if the current state matches what we're trying to set
	if c.State() == newState {
		return nil
	}
	return ErrReferrersCapabilityAlreadySet
}

// ReferrerMergePool manages concurrent updates to referrers indices.
// It provides a way to merge concurrent tag schema updates for the same
// subject, reducing redundant read-modify-write cycles.
type ReferrerMergePool struct {
	pool syncutil.Pool[syncutil.Merge[referrerChange]]
}

// NewReferrerMergePool creates a new ReferrerMergePool.
func NewReferrerMergePool() *ReferrerMergePool {
	return &ReferrerMergePool{}
}

// Get retrieves or creates a merge operation for the given referrers tag.
// The caller must invoke the returned done function when finished.
func (p *ReferrerMergePool) Get(referrersTag string) (*syncutil.Merge[referrerChange], func()) {
	return p.pool.Get(referrersTag)
}

// Do executes a referrer change operation with automatic merging of concurrent
// updates to the same referrers tag.
//
// Parameters:
//   - referrersTag: The tag identifying the referrers index
//   - change: The referrer change to apply
//   - prepare: Function to fetch the current referrers index (called once per batch)
//   - update: Function to apply all batched changes and push the updated index
//
// Returns any error from prepare or update functions.
func (p *ReferrerMergePool) Do(
	referrersTag string,
	change referrerChange,
	prepare func() error,
	update func(changes []referrerChange) error,
) error {
	merge, done := p.pool.Get(referrersTag)
	defer done()
	return merge.Do(change, prepare, update)
}
