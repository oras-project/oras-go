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
	"reflect"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/internal/descriptor"
)

func TestGenerateDescriptor(t *testing.T) {
	contentFoo := []byte("foo")
	contentBar := []byte("bar")

	type args struct {
		content   []byte
		mediaType string
	}
	tests := []struct {
		name string
		args args
		want ocispec.Descriptor
	}{
		{
			name: "foo descriptor",
			args: args{contentFoo, "example media type"},
			want: ocispec.Descriptor{
				MediaType: "example media type",
				Digest:    digest.FromBytes(contentFoo),
				Size:      int64(len(contentFoo))},
		},
		{
			name: "empty content",
			args: args{[]byte(""), "example media type"},
			want: ocispec.Descriptor{
				MediaType: "example media type",
				Digest:    digest.FromBytes([]byte("")),
				Size:      int64(len([]byte("")))},
		},
		{
			name: "missing media type",
			args: args{contentBar, ""},
			want: ocispec.Descriptor{
				MediaType: descriptor.DefaultMediaType,
				Digest:    digest.FromBytes(contentBar),
				Size:      int64(len(contentBar))},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NewDescriptorFromBytes(tt.args.mediaType, tt.args.content)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GenerateDescriptor() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEqual(t *testing.T) {
	contentFoo := []byte("foo")
	contentBar := []byte("bar")
	type args struct {
		a ocispec.Descriptor
		b ocispec.Descriptor
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "same media type, digest and size",
			args: args{
				ocispec.Descriptor{
					MediaType: "example media type",
					Digest:    digest.FromBytes(contentFoo),
					Size:      int64(len(contentFoo))},
				ocispec.Descriptor{
					MediaType: "example media type",
					Digest:    digest.FromBytes(contentFoo),
					Size:      int64(len(contentFoo))}},
			want: true,
		},
		{
			name: "different media type, same digest and size",
			args: args{
				ocispec.Descriptor{
					MediaType: "example media type",
					Digest:    digest.FromBytes(contentFoo),
					Size:      int64(len(contentFoo))},
				ocispec.Descriptor{
					MediaType: "another media type",
					Digest:    digest.FromBytes(contentFoo),
					Size:      int64(len(contentFoo))}},
			want: false,
		},
		{
			name: "different digest, same media type and size",
			args: args{
				ocispec.Descriptor{
					MediaType: "example media type",
					Digest:    digest.FromBytes(contentFoo),
					Size:      int64(len(contentFoo))},
				ocispec.Descriptor{
					MediaType: "example media type",
					Digest:    digest.FromBytes(contentBar),
					Size:      int64(len(contentBar))}},
			want: false,
		},
		{
			name: "only same media type",
			args: args{
				ocispec.Descriptor{
					MediaType: "example media type",
					Digest:    digest.FromBytes([]byte("fooooo")),
					Size:      int64(len([]byte("foooo")))},
				ocispec.Descriptor{
					MediaType: "example media type",
					Digest:    digest.FromBytes(contentBar),
					Size:      int64(len(contentBar))}},
			want: false,
		},
		{
			name: "different size, same media type and digest",
			args: args{
				ocispec.Descriptor{
					MediaType: "example media type",
					Digest:    digest.FromBytes(contentFoo),
					Size:      int64(len(contentFoo))},
				ocispec.Descriptor{
					MediaType: "example media type",
					Digest:    digest.FromBytes(contentFoo),
					Size:      int64(len(contentFoo)) + 1}},
			want: false,
		},
		{
			name: "two empty descriptors",
			args: args{
				ocispec.Descriptor{},
				ocispec.Descriptor{}},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Equal(tt.args.a, tt.args.b); got != tt.want {
				t.Errorf("Equal() = %v, want %v", got, tt.want)
			}
		})
	}
}
