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

package models

import (
	"context"
)

// Reference represents a named reference to a manifest (tag or other reference).
// Reference is safe for concurrent use.
type Reference struct {
	name     string
	manifest lazy[Manifest]
	client   ManifestClient
}

// NewReference creates a new Reference.
// If manifest is non-nil, it is pre-cached (Resolve will return it immediately).
func NewReference(name string, manifest Manifest, client ManifestClient) *Reference {
	ref := &Reference{
		name:   name,
		client: client,
	}
	if manifest != nil {
		ref.manifest.set(manifest)
	}
	return ref
}

// Name returns the reference name (e.g., tag name).
func (r *Reference) Name() string {
	return r.name
}

// Resolve resolves the reference to a manifest.
// If the manifest is not yet loaded, it will be fetched via the client.
// Concurrent calls are safe; only one fetch will occur.
func (r *Reference) Resolve(ctx context.Context) (Manifest, error) {
	return r.manifest.get(func() (Manifest, error) {
		if r.client == nil {
			return nil, ErrNoClient
		}
		return r.client.FetchByReference(ctx, r.name)
	})
}

// Tag tags the given manifest with this reference name.
func (r *Reference) Tag(ctx context.Context, manifest Manifest) error {
	if r.client == nil {
		return ErrNoClient
	}

	if err := r.client.PushManifest(ctx, manifest, r.name); err != nil {
		return err
	}

	r.manifest.set(manifest)
	return nil
}

// Manifest returns the cached manifest if already resolved.
// Returns (manifest, true) if resolved, or (nil, false) if not yet resolved.
func (r *Reference) Manifest() (Manifest, bool) {
	return r.manifest.peek()
}
