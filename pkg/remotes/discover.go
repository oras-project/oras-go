package remotes

import (
	"context"
	"fmt"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	v1 "github.com/oras-project/artifacts-spec/specs-go/v1"
)

// TODO: WIP, probably need to discuss this API since it is new
type DiscoveredArtifact struct {
	Digest   digest.Digest
	Manifest v1.Descriptor
}

type DiscoverFunc func(ctx context.Context, desc ocispec.Descriptor, artifactType string) ([]DiscoveredArtifact, error)

type Discoverer interface {
	Discover(ctx context.Context, desc ocispec.Descriptor, artifactType string) ([]DiscoveredArtifact, error)
}

func (r resolver) Discoverer(ctx context.Context, ref string) (Discoverer, error) {
	if r.discoverer == nil {
		return nil, fmt.Errorf("Discoverer is disabled")
	}
	return r, nil
}

func (r resolver) Discover(ctx context.Context, desc ocispec.Descriptor, artifactType string) ([]DiscoveredArtifact, error) {
	return r.discoverer(ctx, desc, artifactType)
}
