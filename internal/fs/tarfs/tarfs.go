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

package tarfs

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
)

// blockSize is the size of each block in a tarball.
const blockSize int64 = 512

// tarFS represents a file system (an fs.FS) based from a tarball.
type tarFS struct {
	// path is the path to the tarball.
	path string
	// entries is the map of entry name to its position.
	entries map[string]int64
}

// New returns a file system (an fs.FS) for a tarball located at path.
func New(path string) (*tarFS, error) {
	tarfs := &tarFS{
		path:    path,
		entries: make(map[string]int64),
	}
	if err := tarfs.indexEntries(); err != nil {
		return nil, err
	}
	return tarfs, nil
}

// indexEntries index entries in the tarball.
func (tfs *tarFS) indexEntries() error {
	file, err := os.Open(tfs.path)
	if err != nil {
		return err
	}
	defer file.Close()

	tr := tar.NewReader(file)
	for {
		header, err := tr.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return err
		}

		// TODO: symbolic link? check types?
		// if header.Typeflag != tar.TypeReg {
		// 	continue
		// }
		pos, err := file.Seek(0, io.SeekCurrent)
		if err != nil {
			return err
		}
		tfs.entries[header.Name] = pos - blockSize
	}

	return nil
}

// Open opens the named file.
// When Open returns an error, it should be of type *PathError
// with the Op field set to "open", the Path field set to name,
// and the Err field describing the problem.
//
// Open should reject attempts to open names that do not satisfy
// ValidPath(name), returning a *PathError with Err set to
// ErrInvalid or ErrNotExist.
func (tfs *tarFS) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Path: name, Err: fs.ErrInvalid}
	}

	tarFile, err := os.Open(tfs.path)
	if err != nil {
		return nil, err
	}
	defer tarFile.Close()

	pos, ok := tfs.entries[name]
	if !ok {
		return nil, &fs.PathError{Path: name, Err: fs.ErrNotExist}
	}
	if _, err := tarFile.Seek(pos, io.SeekStart); err != nil {
		return nil, err
	}

	tr := tar.NewReader(tarFile)
	header, err := tr.Next()
	if err != nil {
		return nil, err
	}
	if header.Typeflag != tar.TypeReg {
		// TODO: check type?
		return nil, err
	}

	data, err := io.ReadAll(tr)
	if err != nil {
		return nil, err
	}
	entry := &entry{
		header: header,
		r:      bytes.NewReader(data),
	}
	return entry, nil
}

// entry represents an entry in a tarball.
type entry struct {
	header *tar.Header
	r      io.Reader
}

// Stat returns a fs.FileInfo describing e.
func (e *entry) Stat() (fs.FileInfo, error) {
	return e.header.FileInfo(), nil
}

// Read reads e.
func (e *entry) Read(b []byte) (int, error) {
	return e.r.Read(b)
}

// Close closes e.
func (e *entry) Close() error {
	return nil
}
