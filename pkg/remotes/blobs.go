package remotes

import (
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

type blob struct {
	ref reference
}

func (b blob) fetch(ctx context.Context, client *http.Client) (io.ReadCloser, error) {
	request, err := endpoints.e2HEAD.prepareWithDescriptor()(ctx, r.host, r.namespace, desc)
	if err != nil {
		return err
	}

	resp, err := r.client.Do(request)
	if err != nil {
		return nil, err
	}

	request, err = endpoints.e2GET.prepareWithDescriptor()(ctx, r.host, r.namespace, desc)
	if err != nil {
		return nil, err
	}

	resp, err = r.client.Do(request)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("could not fetch content")
	}

	return request.Body, nil
}
