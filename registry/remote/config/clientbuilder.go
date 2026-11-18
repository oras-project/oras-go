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

package config

import (
	"github.com/oras-project/oras-go/v3/registry/remote"
	"github.com/oras-project/oras-go/v3/registry/remote/credentials"
	"github.com/oras-project/oras-go/v3/registry/remote/policy"
	"github.com/oras-project/oras-go/v3/registry/remote/signature"
)

// ClientBuilderOptions configures [Configs.ClientBuilder].
type ClientBuilderOptions struct {
	// CredentialStoreOptions is passed to [Configs.CredentialStore].
	CredentialStoreOptions credentials.StoreOptions
}

// ClientBuilder returns a [remote.ClientBuilder] that starts from
// [remote.NewClientBuilder] and applies the loaded configs:
//   - CredentialStore is set when a Docker config or containers auth.json is
//     loaded.
//   - PolicyEvaluator is set when PolicyConfig is loaded, with a signedBy
//     verifier backed by RegistriesDConfig.
//
// It returns an error instead of a builder without the policy when the policy
// evaluator cannot be created. To verify sigstoreSigned requirements, replace
// PolicyEvaluator on the returned builder.
func (c *Configs) ClientBuilder(opts ClientBuilderOptions) (*remote.ClientBuilder, error) {
	builder := remote.NewClientBuilder()
	if c.DockerConfig != nil || c.ContainersAuthConfig != nil || len(c.ContainersAuthConfigs) > 0 {
		store, err := c.CredentialStore(opts.CredentialStoreOptions)
		if err != nil {
			return nil, err
		}
		builder.CredentialStore = store
	}
	if c.PolicyConfig != nil {
		verifier := signature.NewSignedByVerifierFromConfig(c.RegistriesDConfig)
		evaluator, err := c.PolicyEvaluator(policy.WithSignedByVerifier(verifier))
		if err != nil {
			return nil, err
		}
		builder.PolicyEvaluator = evaluator
	}

	return builder, nil
}
