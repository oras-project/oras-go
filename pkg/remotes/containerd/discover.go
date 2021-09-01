package remotes

import (
	"context"
	"fmt"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	orasRemotes "oras.land/oras-go/pkg/remotes"
)

func (r resolver) Discoverer(ctx context.Context, ref string) (orasRemotes.Discoverer, error) {
	if r.discoverer == nil {
		return nil, fmt.Errorf("Discoverer is disabled")
	}
	return r, nil
}

func (r resolver) Discover(ctx context.Context, desc ocispec.Descriptor, artifactType string) (*orasRemotes.Artifacts, error) {
	return r.discoverer(ctx, desc, artifactType)
}
