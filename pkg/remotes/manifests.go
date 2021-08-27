package remotes

type manifests struct {
	ref reference
}

// getDescriptor tries to resolve the reference to a descriptor using the headers
func (m manifests) getDescriptor(ctx context.Context, client *http.Client) (ocispec.Descriptor, error) {
	if r == nil {
		return ocispec.Descriptor{}, fmt.Errorf("reference is nil")
	}

	request, err := endpoints.e3HEAD.prepare()(ctx, r.host, r.namespace, r.ref)
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
func (r *Registry) getDescriptorWithManifest(ctx context.Context) (*ocispec.Manifest, error) {
	if r == nil {
		return nil, fmt.Errorf("reference to this registry pointer is nil")
	}

	// If we didn't get a digest by this point, we need to pull the manifest
	request, err := endpoints.e3GET.prepare()(ctx, r.host, r.namespace, r.ref)
	if err != nil {
		return nil, err
	}

	resp, err := r.client.Do(request)
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
