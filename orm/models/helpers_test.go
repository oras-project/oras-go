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

package models_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3/content/memory"
	"github.com/oras-project/oras-go/v3/orm/models"
)

// newBlobFromBytes creates a Blob from raw bytes using the public constructor.
func newBlobFromBytes(mediaType string, data []byte) *models.Blob {
	return models.NewBlobFromBytes(mediaType, data)
}

// newBlobNoFetcher creates a Blob from a descriptor with no fetcher or pusher.
// This simulates a blob that has not been loaded and cannot be fetched.
func newBlobNoFetcher(mediaType string, data []byte) *models.Blob {
	desc := ocispec.Descriptor{
		MediaType: mediaType,
		Digest:    digest.FromBytes(data),
		Size:      int64(len(data)),
	}
	return models.NewBlob(desc, nil, nil)
}

// newBlobWithFetcher creates a Blob backed by a memory store.
// If failFirst is true, a blob is created with no fetcher to simulate a
// transient error scenario.
func newBlobWithFetcher(mediaType string, data []byte, failFirst bool) *models.Blob {
	if failFirst {
		// Return a blob with no fetcher to simulate transient failure.
		return newBlobNoFetcher(mediaType, data)
	}
	store := memory.New()
	desc := ocispec.Descriptor{
		MediaType: mediaType,
		Digest:    digest.FromBytes(data),
		Size:      int64(len(data)),
	}
	ctx := context.Background()
	if err := store.Push(ctx, desc, bytes.NewReader(data)); err != nil {
		panic("failed to push to memory store: " + err.Error())
	}
	return models.NewBlob(desc, store, store)
}

// newMemoryStore creates a new memory store for testing.
func newMemoryStore() *memory.Store {
	return memory.New()
}

// pushToStore pushes content to a memory store and returns its descriptor.
func pushToStore(t *testing.T, ctx context.Context, store *memory.Store, mediaType string, data []byte) ocispec.Descriptor {
	t.Helper()
	desc := ocispec.Descriptor{
		MediaType: mediaType,
		Digest:    digest.FromBytes(data),
		Size:      int64(len(data)),
	}
	if err := store.Push(ctx, desc, bytes.NewReader(data)); err != nil {
		t.Fatalf("failed to push to memory store: %v", err)
	}
	return desc
}

// newBlobWithStore creates a Blob backed by a memory store with
// the given descriptor.
func newBlobWithStore(desc ocispec.Descriptor, store *memory.Store) *models.Blob {
	return models.NewBlob(desc, store, store)
}

// errNoFetcher returns the sentinel error for missing fetcher.
func errNoFetcher() error {
	return models.ErrNoFetcher
}

// errNoPusher returns the sentinel error for missing pusher.
func errNoPusher() error {
	return models.ErrNoPusher
}
