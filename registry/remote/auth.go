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

package remote

import (
	"context"
	"errors"
	"fmt"

	"github.com/oras-project/oras-go/v3/registry/remote/auth"
	"github.com/oras-project/oras-go/v3/registry/remote/credentials"
	"github.com/oras-project/oras-go/v3/registry/remote/properties"
)

// ErrClientTypeUnsupported is thrown by Login() when the registry's client type
// is not supported.
var ErrClientTypeUnsupported = errors.New("client type not supported")

// Login provides the login functionality with the given credentials. The target
// registry's client should be nil or of type *auth.Client. Login uses
// a client local to the function and will not modify the original client of
// the registry.
func Login(ctx context.Context, store credentials.Store, reg *Registry, cred properties.Credential) error {
	// create a clone of the original registry for login purpose
	regClone := *reg
	// we use the original client if applicable, otherwise use a default client
	var authClient auth.Client
	if reg.Client == nil {
		authClient = *auth.DefaultClient
		authClient.Cache = nil // no cache
	} else if client, ok := reg.Client.(*auth.Client); ok {
		authClient = *client
	} else {
		return ErrClientTypeUnsupported
	}
	regClone.Client = &authClient
	// update credentials with the client
	authClient.CredentialFunc = credentials.StaticCredentialFunc(reg.Reference.Registry, cred)
	// validate and store the credential
	if err := regClone.Ping(ctx); err != nil {
		return fmt.Errorf("failed to validate the credentials for %s: %w", regClone.Reference.Registry, err)
	}
	hostname := ServerAddressFromRegistry(regClone.Reference.Registry)
	if err := store.Put(ctx, hostname, cred); err != nil {
		return fmt.Errorf("failed to store the credentials for %s: %w", hostname, err)
	}
	return nil
}

// Logout provides the logout functionality given the registry name.
func Logout(ctx context.Context, store credentials.Store, registryName string) error {
	registryName = ServerAddressFromRegistry(registryName)
	if err := store.Delete(ctx, registryName); err != nil {
		return fmt.Errorf("failed to delete the credential for %s: %w", registryName, err)
	}
	return nil
}

// GetCredentialFunc returns a GetCredentialFunc() function that can be used by auth.Client.
func GetCredentialFunc(store credentials.Store) credentials.CredentialFunc {
	return func(ctx context.Context, hostport string) (properties.Credential, error) {
		hostport = ServerAddressFromHostname(hostport)
		if hostport == "" {
			return properties.EmptyCredential, nil
		}
		return store.Get(ctx, hostport)
	}
}

// ServerAddressFromRegistry maps a registry to a server address, which is used as
// a key for credentials store. The Docker CLI expects that the credentials of
// the registry 'registry-1.docker.io' or the alias 'docker.io' will be added
// under the key "https://index.docker.io/v1/".
// See: https://github.com/moby/moby/blob/v24.0.2/registry/config.go#L25-L48
func ServerAddressFromRegistry(registry string) string {
	if registry == "docker.io" ||
		registry == "registry-1.docker.io" {
		return "https://index.docker.io/v1/"
	}
	return registry
}

// ServerAddressFromHostname maps a hostname to a server address, which is used as
// a key for credentials store. It is expected that the traffic targetting the
// host "registry-1.docker.io" will be redirected to "https://index.docker.io/v1/".
// See: https://github.com/moby/moby/blob/v24.0.2/registry/config.go#L25-L48
func ServerAddressFromHostname(hostname string) string {
	if hostname == "registry-1.docker.io" {
		return "https://index.docker.io/v1/"
	}
	return hostname
}
