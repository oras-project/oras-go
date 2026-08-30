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
	"github.com/oras-project/oras-go/v3/internal/syncutil"
)

// referrerMergePool manages concurrent updates to referrers indices.
// It provides a way to merge concurrent tag schema updates for the same
// subject, reducing redundant read-modify-write cycles.
type referrerMergePool struct {
	pool syncutil.Pool[syncutil.Merge[referrerChange]]
}

// newReferrerMergePool creates a new referrerMergePool.
func newReferrerMergePool() *referrerMergePool {
	return &referrerMergePool{}
}

// Get retrieves or creates a merge operation for the given referrers tag.
// The caller must invoke the returned done function when finished.
func (p *referrerMergePool) Get(referrersTag string) (*syncutil.Merge[referrerChange], func()) {
	return p.pool.Get(referrersTag)
}
