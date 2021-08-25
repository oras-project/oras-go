package remotes

import (
	"context"
	"fmt"
	"io"

	"github.com/containerd/containerd/remotes"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// Fetcher returns a new fetcher for the provided reference.
// All content fetched from the returned fetcher will be
// from the namespace referred to by ref.
func (r resolver) Fetcher(ctx context.Context, ref string) (remotes.Fetcher, error) {
	if r.fetcher == nil {
		return nil, fmt.Errorf("Fetcher is disabled")
	}
	return r, nil
}

// Fetch the resource identified by the descriptor.
func (r resolver) Fetch(ctx context.Context, desc ocispec.Descriptor) (io.ReadCloser, error) {
	return r.fetcher(ctx, desc)
}
