package oras_test

import (
	"reflect"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
)

func TestGenerateDescriptor(t *testing.T) {
	type args struct {
		content   []byte
		mediaType string
	}
	tests := []struct {
		name    string
		args    args
		want    ocispec.Descriptor
		wantErr bool
	}{
		{
			name: "foo descriptor",
			args: args{[]byte("foo"), "example media type"},
			want: ocispec.Descriptor{
				MediaType: "example media type",
				Digest:    digest.FromBytes([]byte("foo")),
				Size:      int64(len([]byte("foo")))},
			wantErr: false,
		},
		{
			name: "empty content",
			args: args{[]byte(""), "example media type"},
			want: ocispec.Descriptor{
				MediaType: "example media type",
				Digest:    digest.FromBytes([]byte("")),
				Size:      int64(len([]byte("")))},
			wantErr: false,
		},
		{
			name:    "missing media type",
			args:    args{[]byte("bar"), ""},
			want:    ocispec.Descriptor{},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := oras.GenerateDescriptor(tt.args.content, tt.args.mediaType)
			if (err != nil) != tt.wantErr {
				t.Errorf("GenerateDescriptor() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GenerateDescriptor() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEqual(t *testing.T) {
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
					Digest:    digest.FromBytes([]byte("foo")),
					Size:      int64(len([]byte("foo")))},
				ocispec.Descriptor{
					MediaType: "example media type",
					Digest:    digest.FromBytes([]byte("foo")),
					Size:      int64(len([]byte("foo")))}},
			want: true,
		},
		{
			name: "different media type, same digest and size",
			args: args{
				ocispec.Descriptor{
					MediaType: "example media type",
					Digest:    digest.FromBytes([]byte("foo")),
					Size:      int64(len([]byte("foo")))},
				ocispec.Descriptor{
					MediaType: "another media type",
					Digest:    digest.FromBytes([]byte("foo")),
					Size:      int64(len([]byte("foo")))}},
			want: false,
		},
		{
			name: "different digest, same media type and size",
			args: args{
				ocispec.Descriptor{
					MediaType: "example media type",
					Digest:    digest.FromBytes([]byte("foo")),
					Size:      int64(len([]byte("foo")))},
				ocispec.Descriptor{
					MediaType: "example media type",
					Digest:    digest.FromBytes([]byte("bar")),
					Size:      int64(len([]byte("bar")))}},
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
					Digest:    digest.FromBytes([]byte("bar")),
					Size:      int64(len([]byte("bar")))}},
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
			if got := oras.Equal(tt.args.a, tt.args.b); got != tt.want {
				t.Errorf("Equal() = %v, want %v", got, tt.want)
			}
		})
	}
}
