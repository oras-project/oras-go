package remotes

import (
	"context"
	"fmt"
	"io"
	"net/http"
)

type blob struct {
	ref reference
}

func (b blob) fetch(ctx context.Context, client *http.Client) (io.ReadCloser, error) {
	request, err := endpoints.e2HEAD.prepareWithDescriptor()(ctx, b.ref.add.host, b.ref.add.ns, b.ref.digst.String(), b.ref.media)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(request)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	request, err = endpoints.e2GET.prepareWithDescriptor()(ctx, b.ref.add.host, b.ref.add.ns, b.ref.digst.String(), b.ref.media)
	if err != nil {
		return nil, err
	}

	resp, err = client.Do(request)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("could not fetch content")
	}

	return request.Body, nil
}
