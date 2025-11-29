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

package credentials

import (
	"context"

	"github.com/oras-project/oras-go/v3/registry/remote/properties"
)

// CredentialFunc represents a function that resolves the credential for the
// given registry (i.e. host:port).
//
// [EmptyCredential] is a valid return value and should not be considered as
// an error.
type CredentialFunc func(ctx context.Context, hostport string) (properties.Credential, error)

// StaticCredentialFunc specifies static credentials for the given host.
func StaticCredentialFunc(registry string, cred properties.Credential) CredentialFunc {
	if registry == "docker.io" {
		// it is expected that traffic targeting "docker.io" will be redirected
		// to "registry-1.docker.io"
		// reference: https://github.com/moby/moby/blob/v24.0.0-beta.2/registry/config.go#L25-L48
		registry = "registry-1.docker.io"
	}
	return func(_ context.Context, hostport string) (properties.Credential, error) {
		if hostport == registry {
			return cred, nil
		}
		return properties.EmptyCredential, nil
	}
}
