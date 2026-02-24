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

package orm

import (
	"bytes"
	"container/list"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3"
	"github.com/oras-project/oras-go/v3/content"
	"github.com/oras-project/oras-go/v3/internal/docker"
	"github.com/oras-project/oras-go/v3/internal/spec"
	"github.com/oras-project/oras-go/v3/orm/builders"
	"github.com/oras-project/oras-go/v3/orm/models"
	"github.com/oras-project/oras-go/v3/registry"
)

// cacheEntry is an entry in the LRU identity map cache.
type cacheEntry struct {
	digest  digest.Digest
	content models.Content
}

// Client is the main ORM client for working with OCI content.
// It provides an identity map for caching and manages the lifecycle of models.
type Client struct {
	target oras.Target

	// LRU identity map: digest -> *list.Element (containing cacheEntry).
	// Ensures only one instance per digest with bounded memory usage.
	identityMap map[digest.Digest]*list.Element
	lruList     *list.List
	mu          sync.Mutex

	options ClientOptions
}

// ClientOptions configures the ORM client.
type ClientOptions struct {
	// Cache enables the identity map for caching loaded objects.
	Cache bool

	// MaxCacheSize is the maximum number of entries in the identity map cache.
	// 0 means unlimited (default).
	MaxCacheSize int
}

// DefaultClientOptions returns the default client options.
func DefaultClientOptions() ClientOptions {
	return ClientOptions{
		Cache:        true,
		MaxCacheSize: 0,
	}
}

// ClientOption is a function that configures ClientOptions.
type ClientOption func(*ClientOptions)

// WithCache enables or disables the identity map cache.
func WithCache(enabled bool) ClientOption {
	return func(opts *ClientOptions) {
		opts.Cache = enabled
	}
}

// WithMaxCacheSize sets the maximum number of cached entries.
// 0 means unlimited. Negative values are clamped to 0.
func WithMaxCacheSize(size int) ClientOption {
	return func(opts *ClientOptions) {
		if size < 0 {
			size = 0
		}
		opts.MaxCacheSize = size
	}
}

// NewClient creates a new ORM client.
// Panics if target is nil.
func NewClient(target oras.Target, opts ...ClientOption) *Client {
	if target == nil {
		panic("orm: target must not be nil")
	}

	options := DefaultClientOptions()
	for _, opt := range opts {
		opt(&options)
	}

	return &Client{
		target:      target,
		identityMap: make(map[digest.Digest]*list.Element),
		lruList:     list.New(),
		options:     options,
	}
}

// Target returns the underlying ORAS target.
func (c *Client) Target() oras.Target {
	return c.target
}

// getFromCache retrieves content from the identity map.
// On hit, the entry is promoted to the front of the LRU list.
func (c *Client) getFromCache(dgst digest.Digest) (models.Content, bool) {
	if !c.options.Cache {
		return nil, false
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	elem, ok := c.identityMap[dgst]
	if !ok {
		return nil, false
	}
	c.lruList.MoveToFront(elem)
	return elem.Value.(cacheEntry).content, true
}

// addToCache adds content to the identity map.
// If MaxCacheSize is set and the cache is full, the least recently used entry
// is evicted.
func (c *Client) addToCache(content models.Content) {
	if !c.options.Cache {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	dgst := content.Digest()

	// If already cached, update and promote.
	if elem, ok := c.identityMap[dgst]; ok {
		elem.Value = cacheEntry{digest: dgst, content: content}
		c.lruList.MoveToFront(elem)
		return
	}

	// Add new entry at front.
	elem := c.lruList.PushFront(cacheEntry{digest: dgst, content: content})
	c.identityMap[dgst] = elem

	// Evict oldest if over limit.
	if c.options.MaxCacheSize > 0 && c.lruList.Len() > c.options.MaxCacheSize {
		oldest := c.lruList.Back()
		if oldest != nil {
			c.lruList.Remove(oldest)
			delete(c.identityMap, oldest.Value.(cacheEntry).digest)
		}
	}
}

// NewBlob creates a new Blob from raw bytes.
// The blob is configured with the client's target for push/fetch operations.
func (c *Client) NewBlob(mediaType string, data []byte) *models.Blob {
	blob := models.NewBlobFromBytes(mediaType, data, models.WithStorage(c.target, c.target))
	c.addToCache(blob)
	return blob
}

// FetchBlob fetches a blob by descriptor.
func (c *Client) FetchBlob(ctx context.Context, desc ocispec.Descriptor) (*models.Blob, error) {
	// Check cache first
	if cached, ok := c.getFromCache(desc.Digest); ok {
		if blob, ok := cached.(*models.Blob); ok {
			return blob, nil
		}
	}

	// Create blob (content is lazily loaded)
	blob := models.NewBlob(desc, c.target, c.target)
	c.addToCache(blob)

	return blob, nil
}

// FetchManifest fetches a manifest by descriptor.
// It automatically detects the manifest type and returns the appropriate model.
func (c *Client) FetchManifest(ctx context.Context, desc ocispec.Descriptor) (models.Manifest, error) {
	// Check cache first
	if cached, ok := c.getFromCache(desc.Digest); ok {
		if manifest, ok := cached.(models.Manifest); ok {
			return manifest, nil
		}
	}

	// Determine manifest type from media type
	switch desc.MediaType {
	case spec.MediaTypeArtifactManifest:
		return c.fetchArtifact(ctx, desc)
	case ocispec.MediaTypeImageManifest, docker.MediaTypeManifest:
		return c.fetchImage(ctx, desc)
	case ocispec.MediaTypeImageIndex, docker.MediaTypeManifestList:
		return c.fetchIndex(ctx, desc)
	default:
		// Try to detect by fetching and inspecting the content
		return c.fetchManifestByInspection(ctx, desc)
	}
}

// fetchArtifact fetches an artifact manifest.
func (c *Client) fetchArtifact(ctx context.Context, desc ocispec.Descriptor) (*models.Artifact, error) {
	artifact := models.NewArtifact(desc, c.target, c.target, c)
	c.addToCache(artifact)
	return artifact, nil
}

// fetchImage fetches an image manifest.
func (c *Client) fetchImage(ctx context.Context, desc ocispec.Descriptor) (*models.Image, error) {
	image := models.NewImage(desc, c.target, c.target, c)
	c.addToCache(image)
	return image, nil
}

// fetchIndex fetches an index.
func (c *Client) fetchIndex(ctx context.Context, desc ocispec.Descriptor) (*models.Index, error) {
	index := models.NewIndex(desc, c.target, c.target, c)
	c.addToCache(index)
	return index, nil
}

// fetchManifestByInspection fetches a manifest and inspects its content to determine type.
func (c *Client) fetchManifestByInspection(ctx context.Context, desc ocispec.Descriptor) (models.Manifest, error) {
	// Fetch the content
	manifestBytes, err := content.FetchAll(ctx, c.target, desc)
	if err != nil {
		return nil, err
	}

	// Try to unmarshal and detect type
	var raw map[string]interface{}
	if err := json.Unmarshal(manifestBytes, &raw); err != nil {
		return nil, err
	}

	// Check for artifact manifest (has "artifactType" field)
	if _, ok := raw["artifactType"]; ok {
		return c.fetchArtifact(ctx, desc)
	}

	// Check for index (has "manifests" array)
	if _, ok := raw["manifests"]; ok {
		return c.fetchIndex(ctx, desc)
	}

	// Check for image manifest (has "config" object)
	if _, ok := raw["config"]; ok {
		return c.fetchImage(ctx, desc)
	}

	return nil, errors.New("unknown manifest type")
}

// FetchByReference fetches a manifest by reference (tag or digest).
func (c *Client) FetchByReference(ctx context.Context, ref string) (models.Manifest, error) {
	// Resolve reference to descriptor
	desc, err := c.target.Resolve(ctx, ref)
	if err != nil {
		return nil, err
	}

	// Fetch manifest
	return c.FetchManifest(ctx, desc)
}

// FindPredecessors finds all manifests that reference the given content.
// This implements the ManifestClient interface.
// The underlying target must implement content.PredecessorFinder (e.g.,
// remote.Repository, memory store with graph support) for this to return results.
func (c *Client) FindPredecessors(ctx context.Context, node models.Content) ([]models.Manifest, error) {
	pf, ok := c.target.(content.PredecessorFinder)
	if !ok {
		return nil, nil
	}

	descs, err := pf.Predecessors(ctx, node.Descriptor())
	if err != nil {
		return nil, err
	}

	manifests := make([]models.Manifest, 0, len(descs))
	for _, desc := range descs {
		manifest, err := c.FetchManifest(ctx, desc)
		if err != nil {
			return nil, err
		}
		manifests = append(manifests, manifest)
	}
	return manifests, nil
}

// PushManifest pushes a manifest with a reference.
// This implements the ManifestClient interface.
func (c *Client) PushManifest(ctx context.Context, manifest models.Manifest, reference string) error {
	// Ensure the manifest is loaded before serialization.
	if err := manifest.Load(ctx); err != nil {
		return fmt.Errorf("failed to load manifest for push: %w", err)
	}

	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("failed to marshal manifest: %w", err)
	}

	desc := manifest.Descriptor()
	if err := c.target.Push(ctx, desc, bytes.NewReader(manifestBytes)); err != nil {
		return &models.OrmError{Op: "push_manifest", Digest: desc.Digest, Err: err}
	}

	// Tag if reference is provided
	if reference != "" {
		if err := c.target.Tag(ctx, desc, reference); err != nil {
			return &models.OrmError{Op: "tag", Digest: desc.Digest, Err: err}
		}
	}

	return nil
}

// Evict removes a single entry from the identity map cache by digest.
// Returns true if an entry was evicted, false if not found.
func (c *Client) Evict(dgst digest.Digest) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	elem, ok := c.identityMap[dgst]
	if !ok {
		return false
	}
	c.lruList.Remove(elem)
	delete(c.identityMap, dgst)
	return true
}

// ClearCache clears the identity map cache.
func (c *Client) ClearCache() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.identityMap = make(map[digest.Digest]*list.Element)
	c.lruList.Init()
}

// FindReferrers finds manifests that reference the given content with the
// specified artifactType. If artifactType is empty, all referrers are returned.
// It tries the more efficient registry.ReferrerLister first, then falls back
// to content.PredecessorFinder with manual filtering.
func (c *Client) FindReferrers(ctx context.Context, node models.Content, artifactType string) ([]models.Manifest, error) {
	// Try ReferrerLister first (registry-specific, supports filtering).
	if rl, ok := c.target.(registry.ReferrerLister); ok {
		var descs []ocispec.Descriptor
		err := rl.Referrers(ctx, node.Descriptor(), artifactType, func(refs []ocispec.Descriptor) error {
			descs = append(descs, refs...)
			return nil
		})
		if err != nil {
			return nil, err
		}
		manifests := make([]models.Manifest, 0, len(descs))
		for _, desc := range descs {
			m, err := c.FetchManifest(ctx, desc)
			if err != nil {
				return nil, err
			}
			manifests = append(manifests, m)
		}
		return manifests, nil
	}

	// Fall back to PredecessorFinder (no artifactType filter).
	all, err := c.FindPredecessors(ctx, node)
	if err != nil {
		return nil, err
	}
	if artifactType == "" {
		return all, nil
	}
	// Manual filter by artifactType.
	var filtered []models.Manifest
	for _, m := range all {
		if m.Descriptor().ArtifactType == artifactType {
			filtered = append(filtered, m)
		}
	}
	return filtered, nil
}

// ListTags returns all tags available in the target repository.
// The target must implement registry.TagLister.
func (c *Client) ListTags(ctx context.Context) ([]string, error) {
	tl, ok := c.target.(registry.TagLister)
	if !ok {
		return nil, errors.New("target does not support tag listing")
	}
	var tags []string
	err := tl.Tags(ctx, "", func(t []string) error {
		tags = append(tags, t...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return tags, nil
}

// Builder convenience methods

// BuildArtifact creates a new ArtifactBuilder.
func (c *Client) BuildArtifact(artifactType string) *builders.ArtifactBuilder {
	return builders.NewArtifactBuilder(artifactType, c.target, c.target, c)
}

// BuildImage creates a new ImageBuilder.
func (c *Client) BuildImage() *builders.ImageBuilder {
	return builders.NewImageBuilder(c.target, c.target, c)
}

// BuildIndex creates a new IndexBuilder.
func (c *Client) BuildIndex() *builders.IndexBuilder {
	return builders.NewIndexBuilder(c.target, c.target, c)
}
