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

	"oras.land/oras-go/v2/registry/remote/properties"
)

// BobCredential contains authentication credentials used to access remote
// registries.
type BobCredential struct {
	// Username is the name of the user for the remote registry.
	Username string

	// Password is the secret associated with the username.
	Password string

	// RefreshToken is a bearer token to be sent to the authorization service
	// for fetching access tokens.
	// A refresh token is often referred as an identity token.
	// Reference: https://distribution.github.io/distribution/spec/auth/oauth/
	RefreshToken string

	// AccessToken is a bearer token to be sent to the registry.
	// An access token is often referred as a registry token.
	// Reference: https://distribution.github.io/distribution/spec/auth/token/
	AccessToken string
}

// EmptyCredential represents an empty credential.
var EmptyCredential properties.Credential

// CredentialFunc represents a function that resolves the credential for the
// given registry (i.e. host:port).
//
// [EmptyCredential] is a valid return value and should not be considered as
// an error.
type CredentialFunc func(ctx context.Context, hostport string) (properties.Credential, error)

// StaticCredential specifies static credentials for the given host.
func StaticCredential(registry string, cred properties.Credential) CredentialFunc {
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
		return EmptyCredential, nil
	}
}
