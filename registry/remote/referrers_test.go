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
	"errors"
	"reflect"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/internal/spec"
)

func Test_buildReferrersTag(t *testing.T) {
	tests := []struct {
		name    string
		desc    ocispec.Descriptor
		want    string
		wantErr error
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
		{
			name: "bad digest",
			desc: ocispec.Descriptor{
				Digest: "invalid-digest",
			},
			wantErr: digest.ErrDigestInvalidFormat,
		},
		{
			name: "unregistred algorithm: sha1",
			desc: ocispec.Descriptor{
				Digest: "sha1:0ff30941ca5acd879fd809e8c937d9f9e6dd1615",
			},
			wantErr: digest.ErrDigestUnsupported,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildReferrersTag(tt.desc)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("getReferrersTag() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("getReferrersTag() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_isReferrersFilterApplied(t *testing.T) {
	tests := []struct {
		name      string
		applied   string
		requested string
		want      bool
	}{
		{
			name:      "single filter applied, specified filter matches",
			applied:   "artifactType",
			requested: "artifactType",
			want:      true,
		},
		{
			name:      "single filter applied, specified filter does not match",
			applied:   "foo",
			requested: "artifactType",
			want:      false,
		},
		{
			name:      "multiple filters applied, specified filter matches",
			applied:   "foo,artifactType",
			requested: "artifactType",
			want:      true,
		},
		{
			name:      "multiple filters applied, specified filter does not match",
			applied:   "foo,bar",
			requested: "artifactType",
			want:      false,
		},
		{
			name:      "single filter applied, no specified filter",
			applied:   "foo",
			requested: "",
			want:      false,
		},
		{
			name:      "no filter applied, specified filter does not match",
			applied:   "",
			requested: "artifactType",
			want:      false,
		},
		{
			name:      "no filter applied, no specified filter",
			applied:   "",
			requested: "",
			want:      false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isReferrersFilterApplied(tt.applied, tt.requested); got != tt.want {
				t.Errorf("isReferrersFilterApplied() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_filterReferrers(t *testing.T) {
	refs := []ocispec.Descriptor{
		{
			MediaType:    spec.MediaTypeArtifactManifest,
			Size:         1,
			Digest:       digest.FromString("1"),
			ArtifactType: "application/vnd.test",
		},
		{
			MediaType:    spec.MediaTypeArtifactManifest,
			Size:         2,
			Digest:       digest.FromString("2"),
			ArtifactType: "application/vnd.foo",
		},
		{
			MediaType:    spec.MediaTypeArtifactManifest,
			Size:         3,
			Digest:       digest.FromString("3"),
			ArtifactType: "application/vnd.bar",
		},
		{
			MediaType:    spec.MediaTypeArtifactManifest,
			Size:         4,
			Digest:       digest.FromString("4"),
			ArtifactType: "application/vnd.test",
		},
		{
			MediaType:    spec.MediaTypeArtifactManifest,
			Size:         5,
			Digest:       digest.FromString("5"),
			ArtifactType: "application/vnd.baz",
		},
	}
	got := filterReferrers(refs, "application/vnd.test")
	want := []ocispec.Descriptor{
		{
			MediaType:    spec.MediaTypeArtifactManifest,
			Size:         1,
			Digest:       digest.FromString("1"),
			ArtifactType: "application/vnd.test",
		},
		{
			MediaType:    spec.MediaTypeArtifactManifest,
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
			MediaType:    spec.MediaTypeArtifactManifest,
			Size:         1,
			Digest:       digest.FromString("1"),
			ArtifactType: "application/vnd.test",
		},
		{
			MediaType:    spec.MediaTypeArtifactManifest,
			Size:         4,
			Digest:       digest.FromString("2"),
			ArtifactType: "application/vnd.test",
		},
		{
			MediaType:    spec.MediaTypeArtifactManifest,
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
	}

	tests := []struct {
		name            string
		referrers       []ocispec.Descriptor
		referrerChanges []referrerChange
		want            []ocispec.Descriptor
		wantErr         error
	}{
		{
			name:      "add to an empty list",
			referrers: []ocispec.Descriptor{},
			referrerChanges: []referrerChange{
				{descs[0], referrerOperationAdd}, // add new
				{descs[1], referrerOperationAdd}, // add new
				{descs[2], referrerOperationAdd}, // add new
			},
			want: []ocispec.Descriptor{
				descs[0],
				descs[1],
				descs[2],
			},
			wantErr: nil,
		},
		{
			name: "add to a non-empty list",
			referrers: []ocispec.Descriptor{
				descs[0],
				descs[1],
			},
			referrerChanges: []referrerChange{
				{descs[2], referrerOperationAdd}, // add new
				{descs[1], referrerOperationAdd}, // add existing
				{descs[1], referrerOperationAdd}, // add duplicate existing
				{descs[3], referrerOperationAdd}, // add new
				{descs[2], referrerOperationAdd}, // add duplicate new
			},
			want: []ocispec.Descriptor{
				descs[0],
				descs[1],
				descs[2],
				descs[3],
			},
			wantErr: nil,
		},
		{
			name: "partially remove",
			referrers: []ocispec.Descriptor{
				descs[0],
				descs[1],
				descs[2],
			},
			referrerChanges: []referrerChange{
				{descs[2], referrerOperationRemove}, // remove existing
				{descs[1], referrerOperationRemove}, // remove existing
				{descs[3], referrerOperationRemove}, // remove non-existing
				{descs[2], referrerOperationRemove}, // remove duplicate existing
				{descs[4], referrerOperationRemove}, // remove non-existing
			},
			want: []ocispec.Descriptor{
				descs[0],
			},
			wantErr: nil,
		},
		{
			name: "remove all",
			referrers: []ocispec.Descriptor{
				descs[0],
				descs[1],
				descs[2],
			},
			referrerChanges: []referrerChange{
				{descs[2], referrerOperationRemove}, // remove existing
				{descs[0], referrerOperationRemove}, // remove existing
				{descs[1], referrerOperationRemove}, // remove existing
			},
			want:    []ocispec.Descriptor{},
			wantErr: nil,
		},
		{
			name: "add a new one and remove it",
			referrers: []ocispec.Descriptor{
				descs[0],
				descs[1],
				descs[2],
			},
			referrerChanges: []referrerChange{
				{descs[1], referrerOperationAdd},    // add existing
				{descs[3], referrerOperationAdd},    // add new
				{descs[3], referrerOperationAdd},    // add duplicate new
				{descs[3], referrerOperationRemove}, // remove new
				{descs[4], referrerOperationAdd},    // add new
			},
			want: []ocispec.Descriptor{
				descs[0],
				descs[1],
				descs[2],
				descs[4],
			},
			wantErr: nil,
		},
		{
			name: "remove a new one and add it back",
			referrers: []ocispec.Descriptor{
				descs[0],
				descs[1],
				descs[2],
			},
			referrerChanges: []referrerChange{
				{descs[1], referrerOperationAdd},    // add existing
				{descs[3], referrerOperationAdd},    // add new
				{descs[3], referrerOperationRemove}, // remove new,
				{descs[3], referrerOperationAdd},    // add new back
				{descs[4], referrerOperationAdd},    // add new
			},
			want: []ocispec.Descriptor{
				descs[0],
				descs[1],
				descs[2],
				descs[3],
				descs[4],
			},
			wantErr: nil,
		},
		{
			name: "remove an existing one and add it back",
			referrers: []ocispec.Descriptor{
				descs[0],
				descs[1],
				descs[2],
			},
			referrerChanges: []referrerChange{
				{descs[2], referrerOperationRemove}, // remove existing
				{descs[3], referrerOperationAdd},    // add new
				{descs[2], referrerOperationAdd},    // add existing back
			},
			want: []ocispec.Descriptor{
				descs[0],
				descs[1],
				descs[3],
				descs[2],
			},
			wantErr: nil,
		},
		{
			name: "list containing duplicate entries",
			referrers: []ocispec.Descriptor{
				descs[0],
				descs[1],
				descs[0], // duplicate
				descs[2],
				descs[3],
				descs[1], // duplicate
			},
			referrerChanges: []referrerChange{
				{descs[2], referrerOperationAdd},    // add new
				{descs[2], referrerOperationAdd},    // add duplicate new
				{descs[3], referrerOperationRemove}, // remove existing
			},
			want: []ocispec.Descriptor{
				descs[0],
				descs[1],
				descs[2],
			},
			wantErr: nil,
		},
		{
			name: "list containing bad entries",
			referrers: []ocispec.Descriptor{
				descs[0],
				{},
				descs[1],
			},
			referrerChanges: []referrerChange{
				{descs[2], referrerOperationAdd},    // add new
				{descs[1], referrerOperationRemove}, // remove existing
			},
			want: []ocispec.Descriptor{
				descs[0],
				descs[2],
			},
			wantErr: nil,
		},
		{
			name: "no update: same order",
			referrers: []ocispec.Descriptor{
				descs[0],
				descs[1],
				descs[2],
			},
			referrerChanges: []referrerChange{
				{descs[3], referrerOperationAdd},    // add new
				{descs[2], referrerOperationRemove}, // remove existing
				{descs[4], referrerOperationAdd},    // add new
				{descs[4], referrerOperationRemove}, // remove new
				{descs[2], referrerOperationAdd},    // add existing back
				{descs[3], referrerOperationRemove}, // remove new
			},
			want:    nil,
			wantErr: errNoReferrerUpdate,
		},
		{
			name: "no update: different order",
			referrers: []ocispec.Descriptor{
				descs[0],
				descs[1],
				descs[2],
			},
			referrerChanges: []referrerChange{
				{descs[2], referrerOperationRemove}, // remove existing
				{descs[0], referrerOperationRemove}, // remove existing
				{descs[0], referrerOperationAdd},    // add existing back
				{descs[2], referrerOperationAdd},    // add existing back
			},
			want:    nil,
			wantErr: errNoReferrerUpdate, // internal result: 2, 1, 0
		},
		{
			name: "no update: list containing duplicate entries",
			referrers: []ocispec.Descriptor{
				descs[0],
				descs[1],
				descs[0], // duplicate
				descs[2],
				descs[1], // duplicate
			},
			referrerChanges: []referrerChange{
				{descs[2], referrerOperationRemove}, // remove existing
				{descs[0], referrerOperationRemove}, // remove existing
				{descs[0], referrerOperationAdd},    // add existing back
				{descs[2], referrerOperationAdd},    // add existing back
			},
			want: []ocispec.Descriptor{
				descs[1],
				descs[0],
				descs[2],
			},
			wantErr: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := applyReferrerChanges(tt.referrers, tt.referrerChanges)
			if err != tt.wantErr {
				t.Errorf("applyReferrerChanges() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("applyReferrerChanges() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_removeEmptyDescriptors(t *testing.T) {
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
	}
	tests := []struct {
		name  string
		descs []ocispec.Descriptor
		hint  int
		want  []ocispec.Descriptor
	}{
		{
			name:  "empty list",
			descs: []ocispec.Descriptor{},
			hint:  0,
			want:  []ocispec.Descriptor{},
		},
		{
			name:  "all non-empty",
			descs: descs,
			hint:  len(descs),
			want:  descs,
		},
		{
			name: "all empty",
			descs: []ocispec.Descriptor{
				{},
				{},
				{},
			},
			hint: 0,
			want: []ocispec.Descriptor{},
		},
		{
			name: "empty rear",
			descs: []ocispec.Descriptor{
				descs[0],
				{},
				descs[2],
				{},
				{},
			},
			hint: 2,
			want: []ocispec.Descriptor{
				descs[0],
				descs[2],
			},
		},
		{
			name: "empty head",
			descs: []ocispec.Descriptor{
				{},
				descs[0],
				descs[1],
				{},
				{},
				descs[2],
			},
			hint: 3,
			want: []ocispec.Descriptor{
				descs[0],
				descs[1],
				descs[2],
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := removeEmptyDescriptors(tt.descs, tt.hint); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("removeEmptyDescriptors() = %v, want %v", got, tt.want)
			}
		})
	}
}
