/*
   Copyright The containerd Authors.

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

	"github.com/containerd/containerd/remotes/docker"
)

// InitializeOptions takes a set of resolver options and initializes settings for a docker resolver
func InitializeOptions(options *docker.ResolverOptions) *docker.ResolverOptions {
	if options.Tracker == nil {
		options.Tracker = docker.NewInMemoryTracker()
	}

	if options.Headers == nil {
		options.Headers = make(http.Header)
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
