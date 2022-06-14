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

package file

import (
	"os"
	"path/filepath"
	"testing"
)

func Test_ensureBasePath(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "hello world", "foo", "bar"), 0700); err != nil {
		t.Fatal("failed to create temp folders:", err)
	}
	base := "hello world/foo"

	tests := []struct {
		name    string
		target  string
		want    string
		wantErr bool
	}{
		{
			name:   "valid case (depth 0)",
			target: "hello world/foo",
			want:   ".",
		},
		{
			name:   "valid case (depth 1)",
			target: "hello world/foo/bar",
			want:   "bar",
		},
		{
			name:   "valid case (depth 2)",
			target: "hello world/foo/bar/fun",
			want:   filepath.Join("bar", "fun"),
		},
		{
			name:    "invalid prefix",
			target:  "hello world/fun",
			wantErr: true,
		},
		{
			name:    "invalid prefix",
			target:  "hello/foo",
			wantErr: true,
		},
		{
			name:    "bad traversal",
			target:  "hello world/foo/..",
			wantErr: true,
		},
		{
			name:   "valid traversal",
			target: "hello world/foo/../foo/bar/../bar",
			want:   "bar",
		},
		{
			name:    "complex traversal",
			target:  "hello world/foo/../foo/bar/../..",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ensureBasePath(root, base, tt.target)
			if (err != nil) != tt.wantErr {
				t.Errorf("ensureBasePath() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ensureBasePath() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_ensureLinkPath(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "hello world", "foo", "bar"), 0700); err != nil {
		t.Fatal("failed to create temp folders:", err)
	}
	base := "hello world/foo"

	tests := []struct {
		name    string
		link    string
		target  string
		want    string
		wantErr bool
	}{
		{
			name:   "valid case (depth 1)",
			link:   "hello world/foo/bar",
			target: "fun",
			want:   "fun",
		},
		{
			name:   "valid case (depth 2)",
			link:   "hello world/foo/bar/fun",
			target: "../fun",
			want:   "../fun",
		},
		{
			name:    "invalid prefix",
			link:    "hello world/foo",
			target:  "fun",
			wantErr: true,
		},
		{
			name:    "bad traversal",
			link:    "hello world/foo/bar",
			target:  "../fun",
			wantErr: true,
		},
		{
			name:   "valid traversal",
			link:   "hello world/foo/../foo/bar/../bar", // hello world/foo/bar
			target: "../foo/../foo/fun",
			want:   "../foo/../foo/fun",
		},
		{
			name:    "complex traversal",
			link:    "hello world/foo/bar",
			target:  "../foo/../../fun",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ensureLinkPath(root, base, tt.link, tt.target)
			if (err != nil) != tt.wantErr {
				t.Errorf("ensureLinkPath() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ensureLinkPath() = %v, want %v", got, tt.want)
			}
		})
	}
}
