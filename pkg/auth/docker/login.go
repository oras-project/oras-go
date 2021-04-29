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

	ctypes "github.com/docker/cli/cli/config/types"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/registry"
)

// Login logs in to a docker registry identified by the hostname.
func (c *Client) Login(ctx context.Context, hostname, username, secret string, insecure bool) error {
	hostname = resolveHostname(hostname)
	cred := types.AuthConfig{
		Username:      username,
		ServerAddress: hostname,
	}
	if username == "" {
		cred.IdentityToken = secret
	} else {
		cred.Password = secret
	}

	opts := registry.ServiceOptions{}

	if insecure {
		opts.InsecureRegistries = []string{hostname}
	}

	// Login to ensure valid credential
	remote, err := registry.NewService(opts)
	if err != nil {
		return err
	}
	if _, token, err := remote.Auth(ctx, &cred, "oras"); err != nil {
		return err
	} else if token != "" {
		cred.Username = ""
		cred.Password = ""
		cred.IdentityToken = token
	}

	// Store credential
	return c.primaryCredentialsStore(hostname).Store(ctypes.AuthConfig(cred))
}
