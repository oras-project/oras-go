package remotes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

type manifests struct {
	ref reference
}

// getDescriptor tries to resolve the reference to a descriptor using the headers
func (m manifests) getDescriptor(ctx context.Context, client *http.Client) (ocispec.Descriptor, error) {
	request, err := endpoints.e3HEAD.prepare()(ctx, m.ref.add.host, m.ref.add.ns, m.ref.add.loc)
	if err != nil {
		return ocispec.Descriptor{}, err
	}

	resp, err := client.Do(request)
	if err != nil {
		return ocispec.Descriptor{}, err
	}

	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return ocispec.Descriptor{}, fmt.Errorf("non successful error code %d", resp.StatusCode)
	}

	d := resp.Header.Get("Docker-Content-Digest")
	c := resp.Header.Get("Content-Type")
	s := resp.ContentLength

	err = digest.Digest(d).Validate()
	if err == nil && c != "" && s > 0 {
		// TODO: Write annotations
		return ocispec.Descriptor{
			Digest:    digest.Digest(d),
			MediaType: c,
			Size:      s,
		}, nil
	}

	return ocispec.Descriptor{}, err
}

// getDescriptorWithManifest tries to resolve the reference by fetching the manifest
func (m manifests) getDescriptorWithManifest(ctx context.Context, client *http.Client) (*ocispec.Manifest, error) {
	// If we didn't get a digest by this point, we need to pull the manifest
	request, err := endpoints.e3GET.prepare()(ctx, m.ref.add.host, m.ref.add.ns, m.ref.add.loc)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(request)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	manifest := &ocispec.Manifest{}
	err = json.NewDecoder(resp.Body).Decode(manifest)
	if err != nil {
		return nil, err
	}

	return manifest, nil
}
