package remotes

import (
	"context"
	"fmt"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/remotes"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// Pusher returns a new pusher for the provided reference
// The returned Pusher should satisfy content.Ingester and concurrent attempts
// to push the same blob using the Ingester API should result in ErrUnavailable.
func (r resolver) Pusher(ctx context.Context, ref string) (remotes.Pusher, error) {
	if r.pusher == nil {
		return nil, fmt.Errorf("Pusher is disabled")
	}

	return r, nil
}

// Push returns a content writer for the given resource identified
// by the descriptor.
func (r resolver) Push(ctx context.Context, desc ocispec.Descriptor) (content.Writer, error) {
	return r.pusher(ctx, desc)
}
