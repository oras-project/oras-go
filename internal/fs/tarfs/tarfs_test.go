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
	"bytes"
	"errors"
	"io"
	"io/fs"
	"testing"

	"oras.land/oras-go/v2/errdef"
)

/**
test.tar contains:
	foobar
	foobar_link
	foobar_symlink
	dir/
		hello
		subdir/
			world
*/

func TestTarFS_Open_Success(t *testing.T) {
	testFiles := map[string][]byte{
		"foobar":           []byte("foobar"),
		"dir/hello":        []byte("hello"),
		"dir/subdir/world": []byte("world"),
	}
	tfs, err := New("testdata/test.tar")
	if err != nil {
		t.Fatalf("New() error = %v, wantErr %v", err, nil)
	}
	for name, data := range testFiles {
		f, err := tfs.Open(name)
		if err != nil {
			t.Fatalf("TarFS.Open(%s) error = %v, wantErr %v", name, err, nil)
			continue
		}

		got, err := io.ReadAll(f)
		if err != nil {
			t.Fatalf("failed to read %s: %v", name, err)
		}
		f.Close()
		if want := data; !bytes.Equal(got, want) {
			t.Errorf("TarFS.Open(%s) = %v, want %v", name, string(got), string(want))
		}
	}
}

func TestTarFS_Open_NotExist(t *testing.T) {
	testFiles := []string{
		"dir/foo",
		"subdir/bar",
		"barfoo",
	}
	tfs, err := New("testdata/test.tar")
	if err != nil {
		t.Fatalf("New() error = %v, wantErr %v", err, nil)
	}
	for _, name := range testFiles {
		_, err := tfs.Open(name)
		if want := fs.ErrNotExist; !errors.Is(err, want) {
			t.Errorf("TarFS.Open(%s) error = %v, wantErr %v", name, err, want)
		}
	}
}

func TestTarFS_Open_InvalidPath(t *testing.T) {
	testFiles := []string{
		"dir/",
		"subdir/",
		"dir/subdir/",
	}
	tfs, err := New("testdata/test.tar")
	if err != nil {
		t.Fatalf("New() error = %v, wantErr %v", err, nil)
	}
	for _, name := range testFiles {
		_, err := tfs.Open(name)
		if want := fs.ErrInvalid; !errors.Is(err, want) {
			t.Errorf("TarFS.Open(%s) error = %v, wantErr %v", name, err, want)
		}
	}
}

func TestTarFS_Open_Unsupported(t *testing.T) {
	testFiles := []string{
		"foobar_link",
		"foobar_symlink",
	}
	tfs, err := New("testdata/test.tar")
	if err != nil {
		t.Fatalf("New() error = %v, wantErr %v", err, nil)
	}
	for _, name := range testFiles {
		_, err := tfs.Open(name)
		if want := errdef.ErrUnsupported; !errors.Is(err, want) {
			t.Errorf("TarFS.Open(%s) error = %v, wantErr %v", name, err, want)
		}
	}
}
