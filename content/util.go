package content

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/errdef"
)

// unknownConfigMediaType is the default mediaType used when no
// config media type is specified.
const unknownConfigMediaType = "application/vnd.unknown.config.v1+json"

type PackOpts struct {
	ConfigDescriptor    *ocispec.Descriptor
	ConfigMediaType     string
	ConfigAnnotations   map[string]string
	ManifestAnnotations map[string]string
}

func Pack(ctx context.Context, pusher Pusher, layers []ocispec.Descriptor, opts PackOpts) (ocispec.Descriptor, error) {
	configMediaType := unknownConfigMediaType
	if opts.ConfigMediaType != "" {
		configMediaType = opts.ConfigMediaType
	}

	var configDesc ocispec.Descriptor
	if opts.ConfigDescriptor != nil {
		configDesc = *opts.ConfigDescriptor
	} else {
		configBytes := []byte("{}")
		configDesc = ocispec.Descriptor{
			MediaType:   configMediaType,
			Digest:      digest.FromBytes(configBytes),
			Size:        int64(len(configBytes)),
			Annotations: opts.ConfigAnnotations,
		}

		// Store config
		if err := pusher.Push(ctx, configDesc, bytes.NewReader(configBytes)); err != nil && !errors.Is(err, errdef.ErrAlreadyExists) {
			return ocispec.Descriptor{}, fmt.Errorf("failed to store config: %w", err)
		}
	}

	if layers == nil {
		layers = []ocispec.Descriptor{} // make it an empty array to prevent potential server-side bugs
	}

	manifest := ocispec.Manifest{
		Versioned: specs.Versioned{
			SchemaVersion: 2, // historical value. does not pertain to OCI or docker version
		},
		Config:      configDesc,
		MediaType:   ocispec.MediaTypeImageManifest,
		Layers:      layers,
		Annotations: opts.ManifestAnnotations,
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("failed to marshal manifest: %w", err)
	}
	manifestDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(manifestBytes),
		Size:      int64(len(manifestBytes)),
	}

	// Store manifest
	if err := pusher.Push(ctx, manifestDesc, bytes.NewReader(manifestBytes)); err != nil && !errors.Is(err, errdef.ErrAlreadyExists) {
		return ocispec.Descriptor{}, fmt.Errorf("failed to store manifest: %w", err)
	}

	return manifestDesc, nil
}
