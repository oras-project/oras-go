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
	"net/http"
	"strings"

	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/remotes/docker"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// NewResolver returns a new resolver to a Docker registry
func NewOpts(options *docker.ResolverOptions) *docker.ResolverOptions {
	if options == nil {
		options = &docker.ResolverOptions{}
	}

	if options.Tracker == nil {
		options.Tracker = docker.NewInMemoryTracker()
	}

	if options.Headers == nil {
		options.Headers = make(http.Header)
	}
	if _, ok := options.Headers["User-Agent"]; !ok {
		options.Headers.Set("User-Agent", "oras")
	}

	resolveHeader := http.Header{}
	if _, ok := options.Headers["Accept"]; !ok {
		// set headers for all the types we support for resolution.
		resolveHeader.Set("Accept", strings.Join([]string{
			images.MediaTypeDockerSchema2Manifest,
			images.MediaTypeDockerSchema2ManifestList,
			ocispec.MediaTypeImageManifest,
			ocispec.MediaTypeImageIndex, "*/*"}, ", "))
	} else {
		resolveHeader["Accept"] = options.Headers["Accept"]
		delete(options.Headers, "Accept")
	}

	if options.Hosts == nil {
		opts := []docker.RegistryOpt{}
		if options.Host != nil {
			opts = append(opts, docker.WithHostTranslator(options.Host))
		}

		if options.Authorizer == nil {
			options.Authorizer = docker.NewDockerAuthorizer(
				docker.WithAuthClient(options.Client),
				docker.WithAuthHeader(options.Headers),
				docker.WithAuthCreds(options.Credentials))
		}
		opts = append(opts, docker.WithAuthorizer(options.Authorizer))

		if options.Client != nil {
			opts = append(opts, docker.WithClient(options.Client))
		}
		if options.PlainHTTP {
			opts = append(opts, docker.WithPlainHTTP(docker.MatchAllHosts))
		} else {
			opts = append(opts, docker.WithPlainHTTP(docker.MatchLocalhost))
		}
		options.Hosts = docker.ConfigureDefaultRegistries(opts...)
	}

	return options
}
