/*
+Copyright The ORAS Authors.
+Licensed under the Apache License, Version 2.0 (the "License");
+you may not use this file except in compliance with the License.
+You may obtain a copy of the License at
+
+http://www.apache.org/licenses/LICENSE-2.0
+
+Unless required by applicable law or agreed to in writing, software
+distributed under the License is distributed on an "AS IS" BASIS,
+WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
+See the License for the specific language governing permissions and
+limitations under the License.
+*/

package oci

import (
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3/internal/resolver"
)

// manifestCollection is the `manifests` field of `index.json`, kept in step with the
// references of a [resolver.Memory].
//
// Each reference of the resolver owns at most one entry:
//   - a tag owns an entry that carries the tag in its
//     "org.opencontainers.image.ref.name" annotation;
//   - a digest reference owns a plain entry, unless its digest is also tagged,
//     in which case the tagged entries already describe it.
//
// Entries are added, replaced and removed one reference at a time, so syncing
// after a few changes costs a few operations rather than a rebuild. The order
// of the entries is not specified.
//
// A manifestCollection is not safe for concurrent use.
type manifestCollection struct {
	// resolver is the resolver the list mirrors.
	resolver *resolver.Memory
	// manifests are the entries, in the form that is written to `index.json`.
	manifests []ocispec.Descriptor
	// refs[i] is the reference that owns manifests[i].
	refs []string
	// refIndexes maps a reference to the position of its entry in manifests.
	refIndexes map[string]int
}

// newManifestCollection returns an empty list that mirrors src. It is filled by the
// first call of sync.
func newManifestCollection(src *resolver.Memory) *manifestCollection {
	return &manifestCollection{
		resolver: src,
		// not nil, so that an empty list is written as `[]` rather than `null`
		manifests:  []ocispec.Descriptor{},
		refIndexes: make(map[string]int),
	}
}

// sync applies all pending resolver changes to the collection.
// Changes are coalesced by reference, so each reference is reconciled against
// its latest resolver state rather than replaying intermediate operations.
func (m *manifestCollection) sync() {
	m.resolver.Flush(m.apply)
}

// apply reconciles a single reference with its current resolver state.
// It adds, replaces, or removes the reference's manifest entry as needed.
func (m *manifestCollection) apply(c resolver.Change) {
	entry, ok := manifestEntry(c)
	i, exists := m.refIndexes[c.Ref]
	switch {
	case ok && exists:
		m.manifests[i] = entry
	case ok:
		m.refIndexes[c.Ref] = len(m.manifests)
		m.manifests = append(m.manifests, entry)
		m.refs = append(m.refs, c.Ref)
	case exists:
		m.remove(i)
	}
}

// remove removes the entry owned by ref in constant time.
// The last entry is moved into the removed position when necessary, so
// manifest ordering is not preserved.
func (m *manifestCollection) remove(i int) {
	last := len(m.manifests) - 1
	delete(m.refIndexes, m.refs[i])
	if i != last {
		m.manifests[i] = m.manifests[last]
		m.refs[i] = m.refs[last]
		m.refIndexes[m.refs[i]] = i
	}
	// zero the vacated slot, so that it does not retain the removed descriptor
	m.manifests[last] = ocispec.Descriptor{}
	m.manifests = m.manifests[:last]
	m.refs = m.refs[:last]
}

// detach copies the manifest slice so subsequent incremental updates cannot
// mutate a slice that is also referenced by a previously committed index.
func (m *manifestCollection) detach() {
	manifests := make([]ocispec.Descriptor, len(m.manifests))
	copy(manifests, m.manifests)
	m.manifests = manifests
}

// manifestEntry returns the entry that the reference of c owns, and whether it
// owns one.
func manifestEntry(c resolver.Change) (ocispec.Descriptor, bool) {
	if !c.Exists {
		return ocispec.Descriptor{}, false
	}
	if c.Ref == c.Desc.Digest.String() {
		if c.Tagged {
			return ocispec.Descriptor{}, false
		}
		return deleteAnnotationRefName(c.Desc), true
	}
	return withAnnotationRefName(c.Desc, c.Ref), true
}
