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

package config_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/oras-project/oras-go/v3/errdef"
	"github.com/oras-project/oras-go/v3/registry/remote"
	"github.com/oras-project/oras-go/v3/registry/remote/config"
	"github.com/oras-project/oras-go/v3/registry/remote/credentials"
	"github.com/oras-project/oras-go/v3/registry/remote/policy"
)

func loadEmptyAuthConfig(t *testing.T) *config.Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	return cfg
}

func TestConfigs_ClientBuilder_Credentials(t *testing.T) {
	authCfg := loadEmptyAuthConfig(t)
	tests := []struct {
		name      string
		configs   *config.Configs
		wantStore bool
	}{
		{
			name:    "no config",
			configs: &config.Configs{},
		},
		{
			name:      "docker config",
			configs:   &config.Configs{DockerConfig: authCfg},
			wantStore: true,
		},
		{
			name:      "containers auth config only",
			configs:   &config.Configs{ContainersAuthConfig: authCfg},
			wantStore: true,
		},
		{
			name:      "containers auth configs only",
			configs:   &config.Configs{ContainersAuthConfigs: []*config.Config{authCfg}},
			wantStore: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder, err := tt.configs.ClientBuilder(config.ClientBuilderOptions{})
			if err != nil {
				t.Fatalf("ClientBuilder() error = %v", err)
			}
			if got := builder.CredentialStore != nil; got != tt.wantStore {
				t.Errorf("CredentialStore set = %v, want %v", got, tt.wantStore)
			}
			if builder.PolicyEvaluator != nil {
				t.Errorf("PolicyEvaluator = %v, want nil", builder.PolicyEvaluator)
			}
			if builder.BaseTransport != remote.NewClientBuilder().BaseTransport {
				t.Errorf("BaseTransport = %v, want the NewClientBuilder default", builder.BaseTransport)
			}
		})
	}
}

func TestConfigs_ClientBuilder_CredentialStoreOptions(t *testing.T) {
	configs := &config.Configs{DockerConfig: loadEmptyAuthConfig(t)}
	builder, err := configs.ClientBuilder(config.ClientBuilderOptions{
		CredentialStoreOptions: credentials.StoreOptions{
			AllowPlaintextPut:        true,
			IgnoreDefaultNativeStore: true,
		},
	})
	if err != nil {
		t.Fatalf("ClientBuilder() error = %v", err)
	}
	store, ok := builder.CredentialStore.(credentials.Store)
	if !ok {
		t.Fatalf("CredentialStore is %T, want a credentials.Store", builder.CredentialStore)
	}
	cred := credentials.Credential{Username: "user", Password: "pass"}
	if err := store.Put(context.Background(), "registry.example.com", cred); err != nil {
		t.Errorf("Put() error = %v, want nil with AllowPlaintextPut", err)
	}
}

func TestConfigs_ClientBuilder_SignedByVerifier(t *testing.T) {
	configs := &config.Configs{
		PolicyConfig: &policy.Policy{
			Default: policy.PolicyRequirements{
				&policy.PRSignedBy{KeyType: "GPGKeys", KeyData: "not a key"},
			},
		},
	}
	builder, err := configs.ClientBuilder(config.ClientBuilderOptions{})
	if err != nil {
		t.Fatalf("ClientBuilder() error = %v", err)
	}
	if builder.PolicyEvaluator == nil {
		t.Fatal("PolicyEvaluator = nil, want an evaluator")
	}
	image := policy.ImageReference{
		Transport: policy.TransportNameDocker,
		Scope:     "registry.example.com/repo",
		Reference: "registry.example.com/repo@sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
	}
	_, err = builder.PolicyEvaluator.IsImageAllowed(context.Background(), image)
	if errors.Is(err, errdef.ErrUnsupported) {
		t.Fatalf("IsImageAllowed() error = %v, want the signedBy verifier to run", err)
	}
}

func TestConfigs_ClientBuilder_InvalidPolicy(t *testing.T) {
	configs := &config.Configs{PolicyConfig: &policy.Policy{}}
	builder, err := configs.ClientBuilder(config.ClientBuilderOptions{})
	if err == nil {
		t.Fatal("ClientBuilder() error = nil, want an error for an invalid policy")
	}
	if builder != nil {
		t.Errorf("ClientBuilder() builder = %v, want nil", builder)
	}
}
