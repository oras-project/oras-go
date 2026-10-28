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
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oras-project/oras-go/v3/errdef"
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

func TestTarFS_Open_PAXSizeOnlyInExtendedHeader(t *testing.T) {
	const (
		name = "file"
		data = "hello"
	)
	path := writePAXSizeOnlyTar(t, name, []byte(data))

	tfs, err := New(path)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	fi, err := tfs.Stat(name)
	if err != nil {
		t.Fatal("Stat() error =", err)
	}
	if got, want := fi.Size(), int64(len(data)); got != want {
		t.Errorf("Stat().Size() = %v, want %v", got, want)
	}

	f, err := tfs.Open(name)
	if err != nil {
		t.Fatalf("Open(%s) error = %v", name, err)
	}
	defer f.Close()

	fi, err = f.Stat()
	if err != nil {
		t.Fatal("Open().Stat() error =", err)
	}
	if got, want := fi.Size(), int64(len(data)); got != want {
		t.Errorf("Open().Stat().Size() = %v, want %v", got, want)
	}

	got, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if !bytes.Equal(got, []byte(data)) {
		t.Errorf("Open(%s) = %q, want %q", name, got, data)
	}
}

func TestTarFS_Open_PAXLongName(t *testing.T) {
	name := strings.Repeat("a", 120)
	data := []byte("hello")
	path := writeTarFile(t, &tar.Header{
		Name:     name,
		Mode:     0o644,
		Size:     int64(len(data)),
		Typeflag: tar.TypeReg,
		Format:   tar.FormatPAX,
	}, data)

	tfs, err := New(path)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	f, err := tfs.Open(name)
	if err != nil {
		t.Fatalf("Open(%s) error = %v", name, err)
	}
	defer f.Close()
	got, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("Open() = %q, want %q", got, data)
	}
}

func TestTarFS_Open_GNUFormat(t *testing.T) {
	const name = "gnu-file"
	data := []byte("hello")
	path := writeTarFile(t, &tar.Header{
		Name:     name,
		Mode:     0o644,
		Size:     int64(len(data)),
		Typeflag: tar.TypeReg,
		Format:   tar.FormatGNU,
	}, data)

	tfs, err := New(path)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	f, err := tfs.Open(name)
	if err != nil {
		t.Fatalf("Open(%s) error = %v", name, err)
	}
	defer f.Close()
	got, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("Open() = %q, want %q", got, data)
	}
}

// writePAXSizeOnlyTar writes a tar whose regular-file size is present only in a
// PAX "size" record. The following USTAR header has size 0, matching archives
// for files larger than 8 GiB-1 without writing that much data.
func writePAXSizeOnlyTar(t *testing.T, name string, data []byte) string {
	t.Helper()
	// Leading integer is the full record length in bytes. "11 size=5\n" is 10
	// bytes, so a reader would reject it; "10 size=5\n" is the valid encoding.
	paxRecord := "10 size=5\n"
	if len(data) != 5 {
		t.Fatalf("writePAXSizeOnlyTar: data length = %d, want 5 to match PAX record %q", len(data), paxRecord)
	}

	var buf bytes.Buffer
	buf.Write(ustarHeader("PaxHeaders.0/"+name, int64(len(paxRecord)), tar.TypeXHeader))
	buf.Write(padToBlock([]byte(paxRecord)))
	buf.Write(ustarHeader(name, 0, tar.TypeReg))
	buf.Write(padToBlock(data))
	buf.Write(make([]byte, 512*2)) // end-of-archive

	path := filepath.Join(t.TempDir(), "pax-size.tar")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeTarFile(t *testing.T, hdr *tar.Header, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.tar")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(f)
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func ustarHeader(name string, size int64, typeflag byte) []byte {
	hdr := make([]byte, 512)
	copy(hdr[0:], name)
	copy(hdr[100:], []byte("0000644\x00")) // mode
	copy(hdr[108:], []byte("0000000\x00")) // uid
	copy(hdr[116:], []byte("0000000\x00")) // gid
	copy(hdr[124:], []byte(fmt.Sprintf("%011o\x00", size)))
	copy(hdr[136:], []byte("00000000000\x00")) // mtime
	hdr[156] = typeflag
	copy(hdr[257:], []byte("ustar\x00"))
	copy(hdr[263:], []byte("00"))
	for i := 148; i < 156; i++ {
		hdr[i] = ' '
	}
	var sum int64
	for _, c := range hdr {
		sum += int64(c)
	}
	copy(hdr[148:], []byte(fmt.Sprintf("%06o\x00 ", sum)))
	return hdr
}

func padToBlock(b []byte) []byte {
	n := len(b)
	if n%512 == 0 {
		return b
	}
	return append(b, make([]byte, 512-n%512)...)
}
