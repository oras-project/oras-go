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

package remote_test

import (
	"errors"
	"testing"

	"github.com/oras-project/oras-go/v3/errdef"
	"github.com/oras-project/oras-go/v3/registry/remote"
	remoteconfig "github.com/oras-project/oras-go/v3/registry/remote/config"
	"github.com/oras-project/oras-go/v3/registry/remote/properties"
)

func TestClientBuilder_NewRepository_RewrittenSourceReference(t *testing.T) {
	registries := &remoteconfig.RegistriesConfig{
		Aliases: map[string]string{
			"app": "charts.example.test/foo/app",
		},
		Registries: []remoteconfig.Registry{{
			Prefix:   "charts.example.test",
			Location: "localhost:5000",
		}},
	}
	noRewriteProps, err := remoteconfig.NewRegistryProperties("charts.example.test/foo/app", &remoteconfig.RegistriesConfig{
		Registries: []remoteconfig.Registry{{Prefix: "charts.example.test"}},
	})
	if err != nil {
		t.Fatalf("NewRegistryProperties() without a location rewrite error = %v", err)
	}
	if noRewriteProps.Source != (properties.Reference{}) {
		t.Fatalf("RegistryProperties.Source without a location rewrite = %#v, want zero value", noRewriteProps.Source)
	}
	props, err := remoteconfig.NewRegistryProperties("app", registries)
	if err != nil {
		t.Fatalf("NewRegistryProperties() error = %v", err)
	}
	wantSource := properties.Reference{
		Registry:   "charts.example.test",
		Repository: "foo/app",
	}
	if props.Source != wantSource {
		t.Fatalf("RegistryProperties.Source = %#v, want %#v", props.Source, wantSource)
	}
	repo, err := remote.NewClientBuilder().NewRepository(props)
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}

	tests := []struct {
		name      string
		reference string
		want      properties.Reference
		wantErr   bool
	}{
		{
			name:      "source tag is re-rooted",
			reference: "charts.example.test/foo/app:1.2.3",
			want: properties.Reference{
				Registry:   "localhost:5000",
				Repository: "foo/app",
				Tag:        "1.2.3",
			},
		},
		{
			name:      "source digest is re-rooted",
			reference: "charts.example.test/foo/app@sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
			want: properties.Reference{
				Registry:   "localhost:5000",
				Repository: "foo/app",
				Digest:     "sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
			},
		},
		{
			name:      "unrelated registry is rejected",
			reference: "other.example.test/foo/app:1.2.3",
			wantErr:   true,
		},
		{
			name:      "unrelated repository is rejected",
			reference: "charts.example.test/other/app:1.2.3",
			wantErr:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := repo.ParseReference(tt.reference)
			if tt.wantErr {
				if !errors.Is(err, errdef.ErrInvalidReference) {
					t.Fatalf("ParseReference() error = %v, want %v", err, errdef.ErrInvalidReference)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseReference() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("ParseReference() = %#v, want %#v", got, tt.want)
			}
		})
	}
}
