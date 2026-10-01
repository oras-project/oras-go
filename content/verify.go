package content

import (
	"context"
	"io"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// Verify checks the integrity of the content against the provided descriptor.
func Verify(ctx context.Context, fetcher Fetcher, desc ocispec.Descriptor) error {
	rc, err := fetcher.Fetch(ctx, desc)
	if err != nil {
		return err
	}
	defer rc.Close()

	vr := NewVerifyReader(rc, desc)
	if _, err = io.Copy(io.Discard, vr); err != nil {
		return err
	}
	return vr.Verify()
}
