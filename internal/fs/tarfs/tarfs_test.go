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
=== Contents of testdata/cleaned_path.tar ===

dir/
	dir/hello
	dir/subdir/
		dir/subdir/world
foobar
foobar_link
foobar_symlink

=== Contents of testdata/prefixed_path.tar ===

./
./dir/
	./dir/hello
	./dir/subdir/
		./dir/subdir/world
./foobar
./foobar_link
./foobar_symlink

*/

func TestTarFS_Open_Success(t *testing.T) {
	testFiles := map[string][]byte{
		"foobar":           []byte("foobar"),
		"dir/hello":        []byte("hello"),
		"dir/subdir/world": []byte("world"),
	}
	tarPaths := []string{
		"testdata/cleaned_path.tar",
		"testdata/prefixed_path.tar",
	}

	for _, tarPath := range tarPaths {
		t.Run(tarPath, func(t *testing.T) {
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
				t.Run(name, func(t *testing.T) {
					f, err := tfs.Open(name)
					if err != nil {
						t.Fatalf("TarFS.Open(%s) error = %v, wantErr %v", name, err, nil)
					}

					fi, err := f.Stat()
					if err != nil {
						t.Fatalf("failed to get FileInfo for %s: %v", name, err)
					}
					if got, want := fi.Name(), filepath.Base(name); got != want {
						t.Errorf("FileInfo.Name() = %v, want %v", got, want)
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
				})
			}
		})
	}
}

func TestTarFS_Open_MoreThanOnce(t *testing.T) {
	tarPaths := []string{
		"testdata/cleaned_path.tar",
		"testdata/prefixed_path.tar",
	}

	for _, tarPath := range tarPaths {
		t.Run(tarPath, func(t *testing.T) {
			tfs, err := New(tarPath)
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
		})
	}
}

func TestTarFS_Open_NotExist(t *testing.T) {
	testFiles := []string{
		"dir/foo",
		"subdir/bar",
		"barfoo",
	}
	tarPaths := []string{
		"testdata/cleaned_path.tar",
		"testdata/prefixed_path.tar",
	}

	for _, tarPath := range tarPaths {
		t.Run(tarPath, func(t *testing.T) {
			tfs, err := New(tarPath)
			if err != nil {
				t.Fatalf("New() error = %v, wantErr %v", err, nil)
			}
			for _, name := range testFiles {
				t.Run(name, func(t *testing.T) {
					_, err := tfs.Open(name)
					if want := fs.ErrNotExist; !errors.Is(err, want) {
						t.Errorf("TarFS.Open(%s) error = %v, wantErr %v", name, err, want)
					}
				})
			}
		})
	}

}

func TestTarFS_Open_InvalidPath(t *testing.T) {
	testFiles := []string{
		"..",
		"../outside",
		"./dir",
		"dir/",
		"dir/subdir/",
		"/absolute/path",
		"dir/../invalid",
		"dir/./invalid",
		"dir//double_slash",
		"dir/subdir/../../invalid",
	}
	tarPaths := []string{
		"testdata/cleaned_path.tar",
		"testdata/prefixed_path.tar",
	}

	for _, tarPath := range tarPaths {
		t.Run(tarPath, func(t *testing.T) {
			tfs, err := New(tarPath)
			if err != nil {
				t.Fatalf("New() error = %v, wantErr %v", err, nil)
			}
			for _, name := range testFiles {
				t.Run(name, func(t *testing.T) {
					_, err := tfs.Open(name)
					if want := fs.ErrInvalid; !errors.Is(err, want) {
						t.Errorf("TarFS.Open(%s) error = %v, wantErr %v", name, err, want)
					}
				})
			}
		})
	}
}

func TestTarFS_Open_Unsupported(t *testing.T) {
	testFiles := []string{
		"foobar_link",
		"foobar_symlink",
	}
	tarPaths := []string{
		"testdata/cleaned_path.tar",
		"testdata/prefixed_path.tar",
	}

	for _, tarPath := range tarPaths {
		t.Run(tarPath, func(t *testing.T) {
			tfs, err := New(tarPath)
			if err != nil {
				t.Fatalf("New() error = %v, wantErr %v", err, nil)
			}
			for _, name := range testFiles {
				t.Run(name, func(t *testing.T) {
					_, err := tfs.Open(name)
					if want := errdef.ErrUnsupported; !errors.Is(err, want) {
						t.Errorf("TarFS.Open(%s) error = %v, wantErr %v", name, err, want)
					}
				})
			}
		})
	}
}

func TestTarFS_Stat(t *testing.T) {
	tarPaths := []string{
		"testdata/cleaned_path.tar",
		"testdata/prefixed_path.tar",
	}

	for _, tarPath := range tarPaths {
		t.Run(tarPath, func(t *testing.T) {
			tfs, err := New(tarPath)
			if err != nil {
				t.Fatalf("New() error = %v, wantErr %v", err, nil)
			}

			name := "foobar"
			t.Run(name, func(t *testing.T) {
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
			})

			name = "dir/hello"
			t.Run(name, func(t *testing.T) {
				fi, err := tfs.Stat(name)
				if err != nil {
					t.Fatal("Stat() error =", err)
				}
				if got, want := fi.Name(), "hello"; got != want {
					t.Errorf("Stat().want() = %v, want %v", got, want)
				}
				if got, want := fi.Size(), int64(5); got != want {
					t.Errorf("Stat().Size() = %v, want %v", got, want)
				}
			})

			name = "dir/subdir/world"
			t.Run(name, func(t *testing.T) {
				fi, err := tfs.Stat(name)
				if err != nil {
					t.Fatal("Stat() error =", err)
				}
				if got, want := fi.Name(), "world"; got != want {
					t.Errorf("Stat().want() = %v, want %v", got, want)
				}
				if got, want := fi.Size(), int64(5); got != want {
					t.Errorf("Stat().Size() = %v, want %v", got, want)
				}
			})
		})
	}
}

func TestTarFS_Stat_NotExist(t *testing.T) {
	testFiles := []string{
		"dir/foo",
		"subdir/bar",
		"barfoo",
	}
	tarPaths := []string{
		"testdata/cleaned_path.tar",
		"testdata/prefixed_path.tar",
	}

	for _, tarPath := range tarPaths {
		t.Run(tarPath, func(t *testing.T) {
			tfs, err := New(tarPath)
			if err != nil {
				t.Fatalf("New() error = %v, wantErr %v", err, nil)
			}
			for _, name := range testFiles {
				t.Run(name, func(t *testing.T) {
					_, err := tfs.Stat(name)
					if want := fs.ErrNotExist; !errors.Is(err, want) {
						t.Errorf("TarFS.Stat(%s) error = %v, wantErr %v", name, err, want)
					}
				})
			}
		})
	}
}

func TestTarFS_Stat_InvalidPath(t *testing.T) {
	testFiles := []string{
		"..",
		"../outside",
		"./dir",
		"dir/",
		"dir/subdir/",
		"/absolute/path",
		"dir/../invalid",
		"dir/./invalid",
		"dir//double_slash",
		"dir/subdir/../../invalid",
	}
	tarPaths := []string{
		"testdata/cleaned_path.tar",
		"testdata/prefixed_path.tar",
	}

	for _, tarPath := range tarPaths {
		t.Run(tarPath, func(t *testing.T) {
			tfs, err := New(tarPath)
			if err != nil {
				t.Fatalf("New() error = %v, wantErr %v", err, nil)
			}
			for _, name := range testFiles {
				t.Run(name, func(t *testing.T) {
					_, err := tfs.Stat(name)
					if want := fs.ErrInvalid; !errors.Is(err, want) {
						t.Errorf("TarFS.Stat(%s) error = %v, wantErr %v", name, err, want)
					}
				})
			}
		})
	}
}

func TestTarFS_Stat_Unsupported(t *testing.T) {
	testFiles := []string{
		"dir",
		"dir/subdir",
		"foobar_link",
		"foobar_symlink",
	}
	tarPaths := []string{
		"testdata/cleaned_path.tar",
		"testdata/prefixed_path.tar",
	}

	for _, tarPath := range tarPaths {
		t.Run(tarPath, func(t *testing.T) {
			tfs, err := New(tarPath)
			if err != nil {
				t.Fatalf("New() error = %v, wantErr %v", err, nil)
			}
			for _, name := range testFiles {
				t.Run(name, func(t *testing.T) {
					_, err := tfs.Stat(name)
					if want := errdef.ErrUnsupported; !errors.Is(err, want) {
						t.Errorf("TarFS.Stat(%s) error = %v, wantErr %v", name, err, want)
					}
				})
			}
		})
	}
}

func TestTarFs_New_Error(t *testing.T) {
	t.Run("not existing path", func(t *testing.T) {
		_, err := New("testdata/ghost.tar")
		if err == nil {
			t.Error("New() error = nil, wantErr = true")
		}
	})

	t.Run("invalid file path", func(t *testing.T) {
		_, err := New(string([]byte{0x00}))
		if err == nil {
			t.Error("New() error = nil, wantErr = true")
		}
	})
}
