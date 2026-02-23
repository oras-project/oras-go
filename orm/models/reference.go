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
type Reference struct {
	name     string
	manifest Manifest
	client   ManifestClient
}

// NewReference creates a new Reference.
func NewReference(name string, manifest Manifest, client ManifestClient) *Reference {
	return &Reference{
		name:     name,
		manifest: manifest,
		client:   client,
	}
}

// Name returns the reference name (e.g., tag name).
func (r *Reference) Name() string {
	return r.name
}

// Resolve resolves the reference to a manifest.
// If the manifest is not yet loaded, it will be fetched via the client.
func (r *Reference) Resolve(ctx context.Context) (Manifest, error) {
	if r.manifest != nil {
		return r.manifest, nil
	}

	if r.client == nil {
		return nil, ErrNoClient
	}

	manifest, err := r.client.FetchByReference(ctx, r.name)
	if err != nil {
		return nil, err
	}

	r.manifest = manifest
	return manifest, nil
}

// Tag tags the given manifest with this reference name.
func (r *Reference) Tag(ctx context.Context, manifest Manifest) error {
	if r.client == nil {
		return ErrNoClient
	}

	r.manifest = manifest
	return r.client.PushManifest(ctx, manifest, r.name)
}

// Manifest returns the manifest associated with this reference.
func (r *Reference) Manifest() Manifest {
	return r.manifest
}
