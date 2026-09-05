//go:build !windows

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

package oci

import (
	"os"
	"path/filepath"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// TestStore_MetadataFilePermission ensures that writing the metadata files
// through a temporary file does not change the permission that os.WriteFile
// produced before, both when the file is created and when it is replaced.
//
// The permission bits of a file are not modelled by Windows, where Go derives
// the mode from the read-only attribute alone, so this is a Unix-only test.
func TestStore_MetadataFilePermission(t *testing.T) {
	// the permission a newly created file is expected to have is the one that
	// os.WriteFile gives it, whatever the umask of the process happens to be.
	probePath := filepath.Join(t.TempDir(), "probe")
	if err := os.WriteFile(probePath, nil, 0666); err != nil {
		t.Fatal("error calling WriteFile(), error =", err)
	}
	probeInfo, err := os.Stat(probePath)
	if err != nil {
		t.Fatal("error calling Stat(), error =", err)
	}
	createdPerm := probeInfo.Mode().Perm()

	tests := []struct {
		name string
		// perm is the permission of the metadata files before the store is
		// opened. Zero means that they do not exist yet.
		perm os.FileMode
		want os.FileMode
	}{
		{
			name: "created files use the permission of os.WriteFile",
			want: createdPerm,
		},
		{
			name: "replaced files keep their own permission",
			perm: 0600,
			want: 0600,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tempDir := t.TempDir()
			indexPath := filepath.Join(tempDir, ocispec.ImageIndexFile)
			layoutPath := filepath.Join(tempDir, ocispec.ImageLayoutFile)
			if tt.perm != 0 {
				indexJSON := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[]}`)
				if err := os.WriteFile(indexPath, indexJSON, tt.perm); err != nil {
					t.Fatal("error calling WriteFile(), error =", err)
				}
				if err := os.Chmod(indexPath, tt.perm); err != nil {
					t.Fatal("error calling Chmod(), error =", err)
				}
				layoutJSON := []byte(`{"imageLayoutVersion":"1.0.0"}`)
				if err := os.WriteFile(layoutPath, layoutJSON, tt.perm); err != nil {
					t.Fatal("error calling WriteFile(), error =", err)
				}
				if err := os.Chmod(layoutPath, tt.perm); err != nil {
					t.Fatal("error calling Chmod(), error =", err)
				}
			}

			s, err := New(tempDir)
			if err != nil {
				t.Fatal("New() error =", err)
			}
			if err := s.SaveIndex(); err != nil {
				t.Fatal("Store.SaveIndex() error =", err)
			}

			for _, path := range []string{indexPath, layoutPath} {
				fi, err := os.Stat(path)
				if err != nil {
					t.Fatal("error calling Stat(), error =", err)
				}
				if got := fi.Mode().Perm(); got != tt.want {
					t.Errorf("%s mode = %v, want %v", filepath.Base(path), got, tt.want)
				}
			}
		})
	}
}

// TestStore_MetadataFilePermission_Unwritable ensures that the permission of a
// metadata file that the caller cannot write is preserved rather than relaxed.
// A mode of zero is a legitimate permission, not the absence of a file.
func TestStore_MetadataFilePermission_Unwritable(t *testing.T) {
	for _, perm := range []os.FileMode{0000, 0444} {
		t.Run(perm.String(), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), ocispec.ImageIndexFile)
			if err := os.WriteFile(path, []byte("{}"), 0666); err != nil {
				t.Fatal("error calling WriteFile(), error =", err)
			}
			if err := os.Chmod(path, perm); err != nil {
				t.Fatal("error calling Chmod(), error =", err)
			}

			if err := writeFileAtomic(path, []byte(`{"replaced":true}`)); err != nil {
				t.Fatal("writeFileAtomic() error =", err)
			}

			fi, err := os.Stat(path)
			if err != nil {
				t.Fatal("error calling Stat(), error =", err)
			}
			if got := fi.Mode().Perm(); got != perm {
				t.Errorf("mode = %v, want %v", got, perm)
			}
		})
	}
}
