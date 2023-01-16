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
	"path/filepath"
	"testing"

	"oras.land/oras-go/v2/errdef"
)

/*
testdata/test.tar contains:

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
	tarPath := "testdata/test.tar"
	tfs, err := New(tarPath)
	if err != nil {
		t.Fatalf("New() error = %v, wantErr %v", err, nil)
	}
	tarPathAbs, err := filepath.Abs(tarPath)
	if err != nil {
		t.Fatal("error calling filepath.Abs(), error =", err)
	}
	if tfs.path != tarPathAbs {
		t.Fatalf("TarFS.path = %s, want %s", tfs.path, tarPathAbs)
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
		if err = f.Close(); err != nil {
			t.Errorf("TarFS.Open(%s).Close() error = %v", name, err)
		}
		if want := data; !bytes.Equal(got, want) {
			t.Errorf("TarFS.Open(%s) = %v, want %v", name, string(got), string(want))
		}
	}
}

func TestTarFS_Open_MoreThanOnce(t *testing.T) {
	tfs, err := New("testdata/test.tar")
	if err != nil {
		t.Fatalf("New() error = %v, wantErr %v", err, nil)
	}

	name := "foobar"
	data := []byte("foobar")
	// open once
	f1, err := tfs.Open(name)
	if err != nil {
		t.Fatalf("1st: TarFS.Open(%s) error = %v, wantErr %v", name, err, nil)
	}

	got, err := io.ReadAll(f1)
	if err != nil {
		t.Fatalf("1st: failed to read %s: %v", name, err)
	}
	if want := data; !bytes.Equal(got, want) {
		t.Errorf("1st: TarFS.Open(%s) = %v, want %v", name, string(got), string(want))
	}

	// open twice
	f2, err := tfs.Open(name)
	if err != nil {
		t.Fatalf("2nd: TarFS.Open(%s) error = %v, wantErr %v", name, err, nil)
	}
	got, err = io.ReadAll(f2)
	if err != nil {
		t.Fatalf("2nd: failed to read %s: %v", name, err)
	}
	if want := data; !bytes.Equal(got, want) {
		t.Errorf("2nd: TarFS.Open(%s) = %v, want %v", name, string(got), string(want))
	}

	// close
	if err = f1.Close(); err != nil {
		t.Errorf("1st TarFS.Open(%s).Close() error = %v", name, err)
	}
	if err = f2.Close(); err != nil {
		t.Errorf("2nd TarFS.Open(%s).Close() error = %v", name, err)
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

func TestTarFS_Stat(t *testing.T) {
	tfs, err := New("testdata/test.tar")
	if err != nil {
		t.Fatalf("New() error = %v, wantErr %v", err, nil)
	}

	name := "foobar"
	fi, err := tfs.Stat(name)
	if err != nil {
		t.Fatal("Stat() error =", err)
	}
	if got, want := fi.Name(), "foobar"; got != want {
		t.Errorf("Stat().want() = %v, want %v", got, want)
	}
	if got, want := fi.Size(), int64(6); got != want {
		t.Errorf("Stat().Size() = %v, want %v", got, want)
	}

	name = "dir/hello"
	fi, err = tfs.Stat(name)
	if err != nil {
		t.Fatal("Stat() error =", err)
	}
	if got, want := fi.Name(), "hello"; got != want {
		t.Errorf("Stat().want() = %v, want %v", got, want)
	}
	if got, want := fi.Size(), int64(5); got != want {
		t.Errorf("Stat().Size() = %v, want %v", got, want)
	}

	name = "dir/subdir/world"
	fi, err = tfs.Stat(name)
	if err != nil {
		t.Fatal("Stat() error =", err)
	}
	if got, want := fi.Name(), "world"; got != want {
		t.Errorf("Stat().want() = %v, want %v", got, want)
	}
	if got, want := fi.Size(), int64(5); got != want {
		t.Errorf("Stat().Size() = %v, want %v", got, want)
	}
}

func TestTarFS_Stat_NotExist(t *testing.T) {
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
		_, err := tfs.Stat(name)
		if want := fs.ErrNotExist; !errors.Is(err, want) {
			t.Errorf("TarFS.Stat(%s) error = %v, wantErr %v", name, err, want)
		}
	}
}

func TestTarFS_Stat_InvalidPath(t *testing.T) {
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
		_, err := tfs.Stat(name)
		if want := fs.ErrInvalid; !errors.Is(err, want) {
			t.Errorf("TarFS.Stat(%s) error = %v, wantErr %v", name, err, want)
		}
	}
}

func TestTarFS_Stat_Unsupported(t *testing.T) {
	testFiles := []string{
		"foobar_link",
		"foobar_symlink",
	}
	tfs, err := New("testdata/test.tar")
	if err != nil {
		t.Fatalf("New() error = %v, wantErr %v", err, nil)
	}
	for _, name := range testFiles {
		_, err := tfs.Stat(name)
		if want := errdef.ErrUnsupported; !errors.Is(err, want) {
			t.Errorf("TarFS.Stat(%s) error = %v, wantErr %v", name, err, want)
		}
	}
}
