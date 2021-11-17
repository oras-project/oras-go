/*
Copyright The ORAS Authors.
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package docker

import (
	"context"
	"io"
	"net/http"
	"strings"

	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/remotes"
	"github.com/containerd/containerd/remotes/docker"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

// Fetcher is a function that returns a remotes.Fetcher interface
func (d *dockerDiscoverer) Fetcher(ctx context.Context, ref string) (remotes.Fetcher, error) {
	d.reference = ref

	return d, nil
}

// Fetch is a function that returns a io.ReadCloser interface
func (d *dockerDiscoverer) Fetch(ctx context.Context, desc ocispec.Descriptor) (io.ReadCloser, error) {
	hosts, err := d.filterHosts(docker.HostCapabilityPull)
	if err != nil {
		return nil, err
	}
	if len(hosts) == 0 {
		return nil, errors.Wrap(errdefs.ErrNotFound, "no pull hosts")
	}

	var errs []error
	for _, host := range hosts {
		var url string

		if strings.Contains(desc.MediaType, "manifest") {
			url, err = d.FormatManifestAPI(host, desc.Digest.String())
			if err != nil {
				return nil, err
			}
		} else {
			url, err = d.FormatBlobAPI(host, desc.Digest.String())
			if err != nil {
				return nil, err
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}

		req.Header.Set("Accept", desc.MediaType)

		resp, err := d.client.Do(ctx, req)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		return resp.Body, nil
	}

	if len(errs) > 1 {
		for _, e := range errs {
			log.G(ctx).WithError(e).Errorf("error fetching artifact manifest")
		}
		return nil, errs[0]
	}

	log.G(ctx).WithField("media-type", desc.MediaType).WithField("digest", desc.Digest).Warnf("Could not fetch artifacts manifest")

	return nil, errdefs.ErrNotFound
}
