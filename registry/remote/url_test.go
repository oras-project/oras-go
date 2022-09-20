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
	"net/url"
	"reflect"
	"testing"

	"oras.land/oras-go/v2/registry"
)

func Test_buildReferrersURL(t *testing.T) {
	ref := registry.Reference{
		Registry:   "localhost",
		Repository: "hello-world",
		Reference:  "sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
	}

	params := []struct {
		name         string
		plainHttp    bool
		artifactType string
		want         string
	}{
		{
			name:         "plain http, no filter",
			plainHttp:    true,
			artifactType: "",
			want:         "http://localhost/v2/hello-world/referrers/sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
		},
		{
			name:         "https, no filter",
			plainHttp:    false,
			artifactType: "",
			want:         "https://localhost/v2/hello-world/referrers/sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
		},
		{
			name:         "plain http, filter",
			plainHttp:    true,
			artifactType: "signature/example",
			want:         "http://localhost/v2/hello-world/referrers/sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9?artifactType=signature%2Fexample",
		},
		{
			name:         "https, filter",
			plainHttp:    false,
			artifactType: "signature/example",
			want:         "https://localhost/v2/hello-world/referrers/sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9?artifactType=signature%2Fexample",
		},
	}
	for _, tt := range params {
		t.Run(tt.name, func(t *testing.T) {
			got := buildReferrersURL(tt.plainHttp, ref, tt.artifactType)
			if !compareUrl(got, tt.want) {
				t.Errorf("buildReferrersURL() = %s, want %s", got, tt.want)
			}
		})
	}
}

func Test_buildReferrersTagSchemaURL(t *testing.T) {
	digestRef := registry.Reference{
		Registry:   "localhost",
		Repository: "hello-world",
		Reference:  "sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
	}
	tagRef := registry.Reference{
		Registry:   "localhost",
		Repository: "hello-world",
		Reference:  "tag",
	}

	params := []struct {
		name      string
		ref       registry.Reference
		plainHttp bool
		want      string
		wantErr   bool
	}{
		{
			name:      "plain http",
			ref:       digestRef,
			plainHttp: true,
			want:      "http://localhost/v2/hello-world/manifests/sha256-b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
			wantErr:   false,
		},
		{
			name:      "https",
			ref:       digestRef,
			plainHttp: false,
			want:      "https://localhost/v2/hello-world/manifests/sha256-b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
			wantErr:   false,
		},
		{
			name:      "invalid reference",
			ref:       tagRef,
			plainHttp: true,
			want:      "",
			wantErr:   true,
		},
	}
	for _, tt := range params {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildReferrersTagSchemaURL(tt.plainHttp, tt.ref)
			if (err != nil) != tt.wantErr {
				t.Errorf("buildReferrersTagSchemaURL() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !compareUrl(got, tt.want) {
				t.Errorf("buildReferrersTagSchemaURL() = %s, want %s", got, tt.want)
			}
		})
	}
}

// compareUrl compares two urls, regardless of query order and encoding
func compareUrl(s1, s2 string) bool {
	u1, err := url.Parse(s1)
	if err != nil {
		return false
	}
	u2, err := url.Parse(s2)
	if err != nil {
		return false
	}
	q1, err := url.ParseQuery(u1.RawQuery)
	if err != nil {
		return false
	}
	q2, err := url.ParseQuery(u2.RawQuery)
	if err != nil {
		return false
	}
	return u1.Scheme == u2.Scheme &&
		reflect.DeepEqual(u1.User, u1.User) &&
		u1.Host == u2.Host &&
		u1.Path == u2.Path &&
		reflect.DeepEqual(q1, q2)
}
