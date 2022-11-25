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
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/errdef"
)

// ReadOnlyStorage is a read-only CAS based on file system with the OCI-Image
// layout.
// Reference: https://github.com/opencontainers/image-spec/blob/master/image-layout.md
type ReadOnlyStorage struct {
	fsys fs.FS
}

// NewStorageFromFS creates a new read-only CAS from fsys.
func NewStorageFromFS(fsys fs.FS) *ReadOnlyStorage {
	return &ReadOnlyStorage{
		fsys: fsys,
	}
}

// Fetch fetches the content identified by the descriptor.
func (s *ReadOnlyStorage) Fetch(_ context.Context, target ocispec.Descriptor) (io.ReadCloser, error) {
	path, err := blobPath(target.Digest)
	if err != nil {
		return nil, fmt.Errorf("%s: %s: %w", target.Digest, target.MediaType, errdef.ErrInvalidDigest)
	}

	fp, err := s.fsys.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%s: %s: %w", target.Digest, target.MediaType, errdef.ErrNotFound)
		}
		return nil, err
	}

	return fp, nil
}

// Exists returns true if the described content Exists.
func (s *ReadOnlyStorage) Exists(_ context.Context, target ocispec.Descriptor) (bool, error) {
	path, err := blobPath(target.Digest)
	if err != nil {
		return false, err
	}

	fp, err := s.fsys.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	defer fp.Close()

	_, err = fp.Stat()
	if err != nil {
		return false, err
	}

	return true, nil
}

// blobPath calculates blob path from the given digest.
func blobPath(dgst digest.Digest) (string, error) {
	if err := dgst.Validate(); err != nil {
		return "", fmt.Errorf("cannot calculate blob path from invalid digest %s: %v", dgst.String(), err)
	}

	// NOTE: DirFS does not support opening paths with Windows separators
	return strings.Join([]string{
		"blobs",
		dgst.Algorithm().String(),
		dgst.Encoded()},
		"/"), nil
}
