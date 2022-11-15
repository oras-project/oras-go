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
	"reflect"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func Test_buildReferrersTag(t *testing.T) {
	tests := []struct {
		name string
		desc ocispec.Descriptor
		want string
	}{
		{
			name: "zero digest",
			desc: ocispec.Descriptor{
				Digest: "sha256:0000000000000000000000000000000000000000000000000000000000000000",
			},
			want: "sha256-0000000000000000000000000000000000000000000000000000000000000000",
		},
		{
			name: "sha256",
			desc: ocispec.Descriptor{
				Digest: "sha256:9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
			},
			want: "sha256-9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
		},
		{
			name: "sha512",
			desc: ocispec.Descriptor{
				Digest: "sha512:ee26b0dd4af7e749aa1a8ee3c10ae9923f618980772e473f8819a5d4940e0db27ac185f8a0e1d5f84f88bc887fd67b143732c304cc5fa9ad8e6f57f50028a8ff",
			},
			want: "sha512-ee26b0dd4af7e749aa1a8ee3c10ae9923f618980772e473f8819a5d4940e0db27ac185f8a0e1d5f84f88bc887fd67b143732c304cc5fa9ad8e6f57f50028a8ff",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := buildReferrersTag(tt.desc); got != tt.want {
				t.Errorf("getReferrersTag() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_isReferrersFilterApplied(t *testing.T) {
	tests := []struct {
		name        string
		annotations map[string]string
		requested   string
		want        bool
	}{
		{
			name:        "single filter applied, specified filter matches",
			annotations: map[string]string{ocispec.AnnotationReferrersFiltersApplied: "artifactType"},
			requested:   "artifactType",
			want:        true,
		},
		{
			name:        "single filter applied, specified filter does not match",
			annotations: map[string]string{ocispec.AnnotationReferrersFiltersApplied: "foo"},
			requested:   "artifactType",
			want:        false,
		},
		{
			name:        "multiple filters applied, specified filter matches",
			annotations: map[string]string{ocispec.AnnotationReferrersFiltersApplied: "foo,artifactType"},
			requested:   "artifactType",
			want:        true,
		},
		{
			name:        "multiple filters applied, specified filter does not match",
			annotations: map[string]string{ocispec.AnnotationReferrersFiltersApplied: "foo,bar"},
			requested:   "artifactType",
			want:        false,
		},
		{
			name:        "single filter applied, specified filter empty",
			annotations: map[string]string{ocispec.AnnotationReferrersFiltersApplied: "foo"},
			requested:   "",
			want:        false,
		},
		{
			name:        "no filter applied",
			annotations: map[string]string{},
			requested:   "artifactType",
			want:        false,
		},
		{
			name:        "empty filter applied",
			annotations: map[string]string{ocispec.AnnotationReferrersFiltersApplied: ""},
			requested:   "artifactType",
			want:        false,
		},
		{
			name:        "no filter applied, specified filter empty",
			annotations: map[string]string{},
			requested:   "",
			want:        false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isReferrersFilterApplied(tt.annotations, tt.requested); got != tt.want {
				t.Errorf("isReferrersFilterApplied() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_filterReferrers(t *testing.T) {
	refs := []ocispec.Descriptor{
		{
			MediaType:    ocispec.MediaTypeArtifactManifest,
			Size:         1,
			Digest:       digest.FromString("1"),
			ArtifactType: "application/vnd.test",
		},
		{
			MediaType:    ocispec.MediaTypeArtifactManifest,
			Size:         2,
			Digest:       digest.FromString("2"),
			ArtifactType: "application/vnd.foo",
		},
		{
			MediaType:    ocispec.MediaTypeArtifactManifest,
			Size:         3,
			Digest:       digest.FromString("3"),
			ArtifactType: "application/vnd.bar",
		},
		{
			MediaType:    ocispec.MediaTypeArtifactManifest,
			Size:         4,
			Digest:       digest.FromString("4"),
			ArtifactType: "application/vnd.test",
		},
		{
			MediaType:    ocispec.MediaTypeArtifactManifest,
			Size:         5,
			Digest:       digest.FromString("5"),
			ArtifactType: "application/vnd.baz",
		},
	}
	got := filterReferrers(refs, "application/vnd.test")
	want := []ocispec.Descriptor{
		{
			MediaType:    ocispec.MediaTypeArtifactManifest,
			Size:         1,
			Digest:       digest.FromString("1"),
			ArtifactType: "application/vnd.test",
		},
		{
			MediaType:    ocispec.MediaTypeArtifactManifest,
			Size:         4,
			Digest:       digest.FromString("4"),
			ArtifactType: "application/vnd.test",
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("filterReferrers() = %v, want %v", got, want)
	}
}

func Test_filterReferrers_allMatch(t *testing.T) {
	refs := []ocispec.Descriptor{
		{
			MediaType:    ocispec.MediaTypeArtifactManifest,
			Size:         1,
			Digest:       digest.FromString("1"),
			ArtifactType: "application/vnd.test",
		},
		{
			MediaType:    ocispec.MediaTypeArtifactManifest,
			Size:         4,
			Digest:       digest.FromString("2"),
			ArtifactType: "application/vnd.test",
		},
		{
			MediaType:    ocispec.MediaTypeArtifactManifest,
			Size:         5,
			Digest:       digest.FromString("3"),
			ArtifactType: "application/vnd.test",
		},
	}
	got := filterReferrers(refs, "application/vnd.test")
	if !reflect.DeepEqual(got, refs) {
		t.Errorf("filterReferrers() = %v, want %v", got, refs)
	}
}

func Test_applyReferrerChanges(t *testing.T) {
	descs := []ocispec.Descriptor{
		{
			MediaType:    ocispec.MediaTypeDescriptor,
			Digest:       "sha256:2c26b46b68ffc68ff99b453c1d30413413422d706483bfa0f98a5e886266e7ae",
			Size:         3,
			ArtifactType: "foo",
			Annotations:  map[string]string{"name": "foo"},
		},
		{
			MediaType:    ocispec.MediaTypeDescriptor,
			Digest:       "sha256:fcde2b2edba56bf408601fb721fe9b5c338d10ee429ea04fae5511b68fbf8fb9",
			Size:         3,
			ArtifactType: "bar",
			Annotations:  map[string]string{"name": "bar"},
		},
		{
			MediaType:    ocispec.MediaTypeDescriptor,
			Digest:       "sha256:baa5a0964d3320fbc0c6a922140453c8513ea24ab8fd0577034804a967248096",
			Size:         3,
			ArtifactType: "baz",
			Annotations:  map[string]string{"name": "baz"},
		},
		{
			MediaType:    ocispec.MediaTypeDescriptor,
			Digest:       "sha256:2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824",
			Size:         5,
			ArtifactType: "hello",
			Annotations:  map[string]string{"name": "hello"},
		},
		{
			MediaType:    ocispec.MediaTypeDescriptor,
			Digest:       "sha256:82e35a63ceba37e9646434c5dd412ea577147f1e4a41ccde1614253187e3dbf9",
			Size:         7,
			ArtifactType: "goodbye",
			Annotations:  map[string]string{"name": "goodbye"},
		},
		{
			MediaType:    ocispec.MediaTypeDescriptor,
			Digest:       "sha256:486ea46224d1bb4fb680f34f7c9ad96a8f24ec88be73ea8e5a6c65260e9cb8a7",
			Size:         5,
			ArtifactType: "world",
			Annotations:  map[string]string{"name": "world"},
		},
	}

	tests := []struct {
		name            string
		referrers       []ocispec.Descriptor
		referrerChanges []referrerChange
		want            []ocispec.Descriptor
	}{
		{
			name: "test addition only",
			referrers: []ocispec.Descriptor{
				descs[0],
				descs[1],
			},
			referrerChanges: []referrerChange{
				{
					referrer:  descs[2], // add new
					operation: referrerOperationAdd,
				},
				{
					referrer:  descs[1], // add existing
					operation: referrerOperationAdd,
				},
				{
					referrer:  descs[1], // add duplicate existing
					operation: referrerOperationAdd,
				},
				{
					referrer:  descs[3], // add new
					operation: referrerOperationAdd,
				},
				{
					referrer:  descs[2], // add duplicate new
					operation: referrerOperationAdd,
				},
			},
			want: []ocispec.Descriptor{
				descs[0],
				descs[1],
				descs[2],
				descs[3],
			},
		},
		{
			name: "test removal only",
			referrers: []ocispec.Descriptor{
				descs[0],
				descs[1],
				descs[2],
			},
			referrerChanges: []referrerChange{
				{
					referrer:  descs[2], // remove existing
					operation: referrerOperationRemove,
				},
				{
					referrer:  descs[1], // remove existing
					operation: referrerOperationRemove,
				},
				{
					referrer:  descs[3], // remove non-existing
					operation: referrerOperationRemove,
				},
				{
					referrer:  descs[2], // remove duplicate existing
					operation: referrerOperationRemove,
				},
				{
					referrer:  descs[4], // remove non-existing
					operation: referrerOperationRemove,
				},
			},
			want: []ocispec.Descriptor{
				descs[0],
			},
		},
		{
			name: "add first, remove later",
			referrers: []ocispec.Descriptor{
				descs[0],
				descs[1],
				descs[2],
			},
			referrerChanges: []referrerChange{
				{
					referrer:  descs[1], // add existing
					operation: referrerOperationAdd,
				},
				{
					referrer:  descs[3], // add new
					operation: referrerOperationAdd,
				},
				{
					referrer:  descs[3], // add duplicate new
					operation: referrerOperationAdd,
				},
				{
					referrer:  descs[3], // remove new
					operation: referrerOperationRemove,
				},
				{
					referrer:  descs[4], // add new
					operation: referrerOperationAdd,
				},
			},
			want: []ocispec.Descriptor{
				descs[0],
				descs[1],
				descs[2],
				descs[4],
			},
		},
		{
			name: "remove first, add later",
			referrers: []ocispec.Descriptor{
				descs[0],
				descs[1],
				descs[2],
			},
			referrerChanges: []referrerChange{
				{
					referrer:  descs[1], // add existing
					operation: referrerOperationAdd,
				},
				{
					referrer:  descs[3], // add new
					operation: referrerOperationAdd,
				},
				{
					referrer:  descs[3], // remove new
					operation: referrerOperationRemove,
				},
				{
					referrer:  descs[3], // add duplicate new
					operation: referrerOperationAdd,
				},

				{
					referrer:  descs[4], // add new
					operation: referrerOperationAdd,
				},
			},
			want: []ocispec.Descriptor{
				descs[0],
				descs[1],
				descs[2],
				descs[3],
				descs[4],
			},
		},
		{
			name: "2 remove",
			referrers: []ocispec.Descriptor{
				descs[0],
				descs[1],
				descs[2],
			},
			referrerChanges: []referrerChange{
				{
					referrer:  descs[2], // remove existing
					operation: referrerOperationRemove,
				},
				{
					referrer:  descs[3], // add new
					operation: referrerOperationAdd,
				},
				{
					referrer:  descs[2], // add existing
					operation: referrerOperationAdd,
				},
			},
			want: []ocispec.Descriptor{
				descs[0],
				descs[1],
				descs[3],
				descs[2],
			},
		},
		{
			name: "no change",
			referrers: []ocispec.Descriptor{
				descs[0],
				descs[1],
				descs[2],
			},
			referrerChanges: []referrerChange{
				{
					referrer:  descs[3], // add new
					operation: referrerOperationAdd,
				},
				{
					referrer:  descs[4], // add new
					operation: referrerOperationAdd,
				},
				{
					referrer:  descs[4], // remove new
					operation: referrerOperationRemove,
				},
				{
					referrer:  descs[3], // remove new
					operation: referrerOperationRemove,
				},
			},
			want: []ocispec.Descriptor{
				descs[0],
				descs[1],
				descs[2],
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := applyReferrerChanges(tt.referrers, tt.referrerChanges)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("applyReferrerChanges() = %v, want %v", got, tt.want)
			}
		})
	}
}
