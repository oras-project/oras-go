package remotes

import (
	"context"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	v1 "github.com/oras-project/artifacts-spec/specs-go/v1"
)

// TODO: WIP, probably need to discuss this API since it is new
type DiscoveredArtifact struct {
	Digest   digest.Digest
	Manifest v1.Descriptor
}

type Artifacts struct {
	Artifacts []DiscoveredArtifact
}

type DiscoverFunc func(ctx context.Context, desc ocispec.Descriptor, artifactType string) (*Artifacts, error)

type Discoverer interface {
	Discover(ctx context.Context, desc ocispec.Descriptor, artifactType string) (*Artifacts, error)
}
