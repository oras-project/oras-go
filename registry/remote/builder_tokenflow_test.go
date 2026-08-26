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
	"testing"

	"github.com/oras-project/oras-go/v3/registry/remote/auth"
	"github.com/oras-project/oras-go/v3/registry/remote/properties"
)

// Test_ClientBuilder_tokenFlowWiring checks that a distribution flow requested
// in registries.conf reaches the auth client, and that an explicitly configured
// TokenFetcher still outranks it.
func Test_ClientBuilder_tokenFlowWiring(t *testing.T) {
	newProps := func(flow properties.TokenFlow) *properties.Registry {
		return &properties.Registry{
			Reference:  properties.Reference{Registry: "registry.example.com", Repository: "app"},
			Attributes: properties.Attributes{TokenFlow: flow},
		}
	}

	t.Run("default leaves the client on its own default", func(t *testing.T) {
		client, err := NewClientBuilder().Build(newProps(properties.TokenFlowDefault))
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		if client.TokenFetcher != nil {
			t.Errorf("TokenFetcher = %T, want nil so the client default (OAuth2) applies", client.TokenFetcher)
		}
	})

	t.Run("oauth2 is the client default, so still nil", func(t *testing.T) {
		client, err := NewClientBuilder().Build(newProps(properties.TokenFlowOAuth2))
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		if client.TokenFetcher != nil {
			t.Errorf("TokenFetcher = %T, want nil", client.TokenFetcher)
		}
	})

	t.Run("distribution installs a legacy composite fetcher", func(t *testing.T) {
		client, err := NewClientBuilder().Build(newProps(properties.TokenFlowDistribution))
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		composite, ok := client.TokenFetcher.(*auth.CompositeTokenFetcher)
		if !ok {
			t.Fatalf("TokenFetcher = %T, want *auth.CompositeTokenFetcher", client.TokenFetcher)
		}
		if !composite.LegacyMode {
			t.Error("LegacyMode = false, want true for token-flow=distribution")
		}
	})

	t.Run("explicit TokenFetcher outranks the config", func(t *testing.T) {
		custom := auth.NewCompositeTokenFetcher(nil, nil, "custom-id", false)
		builder := NewClientBuilder()
		builder.TokenFetcher = custom
		client, err := builder.Build(newProps(properties.TokenFlowDistribution))
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		if client.TokenFetcher != auth.TokenFetcher(custom) {
			t.Error("config token-flow overrode an explicitly configured TokenFetcher")
		}
	})
}
