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
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/internal/spec"
	"oras.land/oras-go/v2/orm/builders"
	"oras.land/oras-go/v2/orm/models"
)

// Client is the main ORM client for working with OCI content.
// It provides an identity map for caching and manages the lifecycle of models.
type Client struct {
	target oras.Target

	// Identity map: digest -> Content
	// Ensures only one instance per digest
	identityMap map[digest.Digest]models.Content
	mu          sync.RWMutex

	options ClientOptions
}

// ClientOptions configures the ORM client.
type ClientOptions struct {
	// Cache enables the identity map for caching loaded objects.
	Cache bool

	// PreloadDepth controls automatic preloading of relationships.
	// 0 = lazy loading (default)
	// 1 = preload direct relationships
	// 2+ = preload nested relationships
	PreloadDepth int

	// Concurrency controls the number of concurrent fetch operations.
	Concurrency int
}

// DefaultClientOptions returns the default client options.
func DefaultClientOptions() ClientOptions {
	return ClientOptions{
		Cache:        true,
		PreloadDepth: 0,
		Concurrency:  3,
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

// WithPreloadDepth sets the automatic preload depth.
func WithPreloadDepth(depth int) ClientOption {
	return func(opts *ClientOptions) {
		opts.PreloadDepth = depth
	}
}

// WithConcurrency sets the concurrent fetch limit.
func WithConcurrency(n int) ClientOption {
	return func(opts *ClientOptions) {
		opts.Concurrency = n
	}
}

// NewClient creates a new ORM client.
func NewClient(target oras.Target, opts ...ClientOption) *Client {
	options := DefaultClientOptions()
	for _, opt := range opts {
		opt(&options)
	}

	return &Client{
		target:      target,
		identityMap: make(map[digest.Digest]models.Content),
		options:     options,
	}
}

// Target returns the underlying ORAS target.
func (c *Client) Target() oras.Target {
	return c.target
}

// getFromCache retrieves content from the identity map.
func (c *Client) getFromCache(dgst digest.Digest) (models.Content, bool) {
	if !c.options.Cache {
		return nil, false
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	content, ok := c.identityMap[dgst]
	return content, ok
}

// addToCache adds content to the identity map.
func (c *Client) addToCache(content models.Content) {
	if !c.options.Cache {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.identityMap[content.Digest()] = content
}

// NewBlob creates a new Blob from raw bytes.
func (c *Client) NewBlob(mediaType string, data []byte) *models.Blob {
	blob := models.NewBlobFromBytes(mediaType, data)
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
	case ocispec.MediaTypeImageManifest, "application/vnd.docker.distribution.manifest.v2+json":
		return c.fetchImage(ctx, desc)
	case ocispec.MediaTypeImageIndex, "application/vnd.docker.distribution.manifest.list.v2+json":
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

// FetchByDigest fetches content by digest.
func (c *Client) FetchByDigest(ctx context.Context, dgst digest.Digest) (models.Content, error) {
	// Check cache first
	if cached, ok := c.getFromCache(dgst); ok {
		return cached, nil
	}

	// We need a descriptor to fetch. This requires the caller to know more info.
	return nil, errors.New("FetchByDigest requires a full descriptor; use FetchBlob or FetchManifest instead")
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
func (c *Client) FindPredecessors(ctx context.Context, content models.Content) ([]models.Manifest, error) {
	// This requires graph storage support
	// For now, return empty list
	// TODO: Implement using content.GraphStorage if available
	return []models.Manifest{}, nil
}

// PushManifest pushes a manifest with a reference.
// This implements the ManifestClient interface.
func (c *Client) PushManifest(ctx context.Context, manifest models.Manifest, reference string) error {
	// Get manifest bytes
	var manifestBytes []byte
	var err error

	switch m := manifest.(type) {
	case *models.Artifact:
		manifestBytes, err = m.MarshalJSON()
	case *models.Image:
		manifestBytes, err = m.MarshalJSON()
	case *models.Index:
		manifestBytes, err = m.MarshalJSON()
	default:
		return errors.New("unsupported manifest type")
	}

	if err != nil {
		return err
	}

	// Pack and push
	desc := manifest.Descriptor()
	if err := c.target.Push(ctx, desc, nil); err != nil {
		return err
	}

	// Tag if reference is provided
	if reference != "" {
		if err := c.target.Tag(ctx, desc, reference); err != nil {
			return err
		}
	}

	return nil
}

// ClearCache clears the identity map cache.
func (c *Client) ClearCache() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.identityMap = make(map[digest.Digest]models.Content)
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
