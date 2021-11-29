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
package content

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"os"

	"github.com/containerd/containerd/remotes"
	"github.com/containerd/containerd/remotes/docker"
	auth "oras.land/oras-go/pkg/auth/docker"
	orasdocker "oras.land/oras-go/pkg/target/docker"
)

// RegistryOptions provide configuration options to a Registry
type RegistryOptions struct {
	Configs   []string
	Username  string
	Password  string
	Insecure  bool
	PlainHTTP bool
}

// Registry provides content from a spec-compliant registry. Create an use a new one for each
// registry with unique configuration of RegistryOptions.
type Registry struct {
	remotes.Resolver
}

// NewRegistry creates a new Registry store
func NewRegistry(opts RegistryOptions) (*Registry, error) {
	return &Registry{
		Resolver: newResolver(opts.Username, opts.Password, opts.Insecure, opts.PlainHTTP, opts.Configs...),
	}, nil
}

func NewRegistryWithDiscover(targetRef string, opts RegistryOptions) (*Registry, error) {
	registry, err := NewRegistry(opts)
	if err != nil {
		return nil, err
	}

	resolverOptions := newResolverOptions(opts.Username, opts.Password, opts.Insecure, opts.Insecure, opts.Configs...)

	resolver, err := orasdocker.WithDiscover(targetRef, registry.Resolver, resolverOptions)
	if err != nil {
		return nil, err
	}

	registry.Resolver = resolver
	return registry, nil
}

func newResolver(username, password string, insecure bool, plainHTTP bool, configs ...string) remotes.Resolver {

	opts := docker.ResolverOptions{
		PlainHTTP: plainHTTP,
	}

	client := http.DefaultClient
	if insecure {
		client.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		}
	}
	opts.Client = client

	if username != "" || password != "" {
		opts.Credentials = func(hostName string) (string, string, error) {
			return username, password, nil
		}
		return docker.NewResolver(opts)
	}
	cli, err := auth.NewClient(configs...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: Error loading auth file: %v\n", err)
	}
	resolver, err := cli.Resolver(context.Background(), client, plainHTTP)
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: Error loading resolver: %v\n", err)
		resolver = docker.NewResolver(opts)
	}
	return resolver
}

func newResolverOptions(username, password string, insecure bool, plainHTTP bool, configs ...string) *docker.ResolverOptions {

	opts := docker.ResolverOptions{
		PlainHTTP: plainHTTP,
	}

	client := http.DefaultClient
	if insecure {
		client.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		}
	}
	opts.Client = client

	if username != "" || password != "" {
		opts.Credentials = func(hostName string) (string, string, error) {
			return username, password, nil
		}
		return &opts
	}
	cli, err := auth.NewClient(configs...)
	if err == nil {
		// Workaround to get the creds with Docker config. The auth should be a separate interface that can let
		// different implementations like Docker config, k8s chain
		if authClient, ok := cli.(*auth.Client); ok {
			opts.Credentials = authClient.Credential
		}
	}

	return &opts
}
