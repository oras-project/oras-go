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

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/remotes"
	"github.com/containerd/containerd/remotes/docker"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	artifactspec "github.com/oras-project/artifacts-spec/specs-go/v1"
	"github.com/pkg/errors"
)

// Pusher is a function that returns a remotes.Pusher interface
func (d *dockerDiscoverer) Pusher(ctx context.Context, ref string) (remotes.Pusher, error) {
	d.reference = ref
	return d, nil
}

// Push is a function that returns a content.Writer
func (d *dockerDiscoverer) Push(ctx context.Context, desc ocispec.Descriptor) (content.Writer, error) {
	switch desc.MediaType {
	case artifactspec.MediaTypeArtifactManifest:
		h, err := d.filterHosts(docker.HostCapabilityPush)
		if err != nil {
			return nil, err
		}

		if len(h) == 0 {
			return nil, errors.New("no host with push")
		}

		host := h[0]

		err = d.CheckManifest(ctx, host, desc)
		if err != nil {
			return nil, err
		}

		return d.PreparePutManifest(ctx, host, desc)
	}

	pusher, err := d.Resolver.Pusher(ctx, d.reference)
	if err != nil {
		return nil, err
	}

	return pusher.Push(ctx, desc)
}
