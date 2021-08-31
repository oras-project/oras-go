package remotes

import (
	"context"
	"errors"
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

	content, err := client.Do(request)
	if err != nil {
		redirectErr, ok := errors.Unwrap(err).(*redirectRequest)
		if ok {
			// Can't use the built in client, because it will add the Authorization header
			// TODO - but still shouldn't use DefaultClient
			content, err = http.DefaultClient.Do(redirectErr.req)
			if err != nil {
				return nil, err
			}
		} else {
			return nil, err
		}
	}

	if content.StatusCode != 200 {
		return nil, fmt.Errorf("could not fetch content")
	}

	return content.Body, nil
}
