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
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func packedDir(t *testing.T, dir, file, content string) (gz []byte, tarDigest digest.Digest) {
	t.Helper()
	raw := createTar(t, []tarEntry{
		{name: dir + "/", mode: os.ModeDir | 0755},
		{name: dir + "/" + file, content: content, mode: 0644},
	})
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(raw); err != nil {
		t.Fatal("gzip.Write() error =", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal("gzip.Close() error =", err)
	}
	return buf.Bytes(), digest.FromBytes(raw)
}

func TestStore_Dir_Add_Annotations(t *testing.T) {
	tempDir := t.TempDir()
	dirName := "testdir"
	dirPath := filepath.Join(tempDir, dirName)
	if err := os.MkdirAll(dirPath, 0777); err != nil {
		t.Fatal("MkdirAll() error =", err)
	}
	if err := os.WriteFile(filepath.Join(dirPath, "test.txt"), []byte("hello world"), 0444); err != nil {
		t.Fatal("WriteFile() error =", err)
	}

	s, err := New(tempDir)
	if err != nil {
		t.Fatal("New() error =", err)
	}
	defer s.Close()

	desc, err := s.Add(context.Background(), dirName, "", dirPath)
	if err != nil {
		t.Fatal("Store.Add() error =", err)
	}

	if got := desc.Annotations[AnnotationUnpack]; got != "true" {
		t.Errorf("%s annotation = %q, want %q", AnnotationUnpack, got, "true")
	}
	if got := desc.Annotations[AnnotationUnpackLegacy]; got != "true" {
		t.Errorf("%s annotation = %q, want %q", AnnotationUnpackLegacy, got, "true")
	}
	newDigest, legacyDigest := desc.Annotations[AnnotationDigest], desc.Annotations[AnnotationDigestLegacy]
	if newDigest == "" {
		t.Errorf("%s annotation is empty", AnnotationDigest)
	}
	if newDigest != legacyDigest {
		t.Errorf("%s annotation = %q, want %q", AnnotationDigestLegacy, legacyDigest, newDigest)
	}
	if newDigest == desc.Digest.String() {
		t.Errorf("%s annotation = %q, want the digest of the uncompressed content", AnnotationDigest, newDigest)
	}
}

func TestStore_Dir_Push_AnnotationKeys(t *testing.T) {
	const (
		dirName  = "testdir"
		fileName = "test.txt"
		content  = "hello world"
	)

	tests := []struct {
		name        string
		annotations func(tarDigest digest.Digest) map[string]string
		skipUnpack  bool
		wantUnpack  bool
		wantErr     bool
	}{
		{
			name: "current keys only",
			annotations: func(d digest.Digest) map[string]string {
				return map[string]string{
					AnnotationUnpack: "true",
					AnnotationDigest: d.String(),
				}
			},
			wantUnpack: true,
		},
		{
			name: "legacy keys only",
			annotations: func(d digest.Digest) map[string]string {
				return map[string]string{
					AnnotationUnpackLegacy: "true",
					AnnotationDigestLegacy: d.String(),
				}
			},
			wantUnpack: true,
		},
		{
			name: "both keys matching",
			annotations: func(d digest.Digest) map[string]string {
				return map[string]string{
					AnnotationUnpack:       "true",
					AnnotationDigest:       d.String(),
					AnnotationUnpackLegacy: "true",
					AnnotationDigestLegacy: d.String(),
				}
			},
			wantUnpack: true,
		},
		{
			name: "both keys conflicting",
			annotations: func(digest.Digest) map[string]string {
				return map[string]string{
					AnnotationUnpack:       "true",
					AnnotationDigest:       digest.FromString("one").String(),
					AnnotationUnpackLegacy: "true",
					AnnotationDigestLegacy: digest.FromString("other").String(),
				}
			},
			wantErr: true,
		},
		{
			name: "conflicting digests without unpack flag",
			annotations: func(digest.Digest) map[string]string {
				return map[string]string{
					AnnotationDigest:       digest.FromString("one").String(),
					AnnotationDigestLegacy: digest.FromString("other").String(),
				}
			},
			wantErr: false,
		},
		{
			name: "conflicting digests are rejected with SkipUnpack",
			annotations: func(digest.Digest) map[string]string {
				return map[string]string{
					AnnotationUnpack:       "true",
					AnnotationDigest:       digest.FromString("one").String(),
					AnnotationUnpackLegacy: "true",
					AnnotationDigestLegacy: digest.FromString("other").String(),
				}
			},
			skipUnpack: true,
			wantErr:    true,
		},
		{
			name: "unpack flag only, no digest",
			annotations: func(digest.Digest) map[string]string {
				return map[string]string{
					AnnotationUnpack: "true",
				}
			},
			wantUnpack: true,
		},
		{
			name: "unpack flag false on both keys",
			annotations: func(d digest.Digest) map[string]string {
				return map[string]string{
					AnnotationUnpack:       "false",
					AnnotationUnpackLegacy: "false",
					AnnotationDigest:       d.String(),
					AnnotationDigestLegacy: d.String(),
				}
			},
			wantUnpack: false,
		},
		{
			name: "conflicting current and legacy unpack flags",
			annotations: func(d digest.Digest) map[string]string {
				return map[string]string{
					AnnotationUnpack:       "false",
					AnnotationUnpackLegacy: "true",
					AnnotationDigest:       d.String(),
					AnnotationDigestLegacy: d.String(),
				}
			},
			wantErr: true,
		},
		{
			name: "conflicting current and legacy digests",
			annotations: func(tarDigest digest.Digest) map[string]string {
				return map[string]string{
					AnnotationUnpack:       "true",
					AnnotationDigest:       digest.FromString("tampered").String(),
					AnnotationUnpackLegacy: "true",
					AnnotationDigestLegacy: tarDigest.String(),
				}
			},
			wantErr: true,
		},
		{
			name: "SkipUnpack keeps the gz",
			annotations: func(d digest.Digest) map[string]string {
				return map[string]string{
					AnnotationUnpack:       "true",
					AnnotationDigest:       d.String(),
					AnnotationUnpackLegacy: "true",
					AnnotationDigestLegacy: d.String(),
				}
			},
			skipUnpack: true,
			wantUnpack: false,
		},
		{
			name: "SkipUnpack with legacy keys only",
			annotations: func(d digest.Digest) map[string]string {
				return map[string]string{
					AnnotationUnpackLegacy: "true",
					AnnotationDigestLegacy: d.String(),
				}
			},
			skipUnpack: true,
			wantUnpack: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gz, tarDigest := packedDir(t, dirName, fileName, content)

			workDir := t.TempDir()
			s, err := New(workDir)
			if err != nil {
				t.Fatal("New() error =", err)
			}
			defer s.Close()
			s.SkipUnpack = tt.skipUnpack

			desc := ocispec.Descriptor{
				MediaType:   ocispec.MediaTypeImageLayerGzip,
				Digest:      digest.FromBytes(gz),
				Size:        int64(len(gz)),
				Annotations: tt.annotations(tarDigest),
			}
			desc.Annotations[ocispec.AnnotationTitle] = dirName

			err = s.Push(context.Background(), desc, bytes.NewReader(gz))
			if tt.wantErr {
				if err == nil {
					t.Fatal("Store.Push() error = nil, wantErr = true")
				}
				return
			}
			if err != nil {
				t.Fatal("Store.Push() error =", err)
			}

			filePath := filepath.Join(workDir, dirName, fileName)
			if tt.wantUnpack {
				got, err := os.ReadFile(filePath)
				if err != nil {
					t.Fatal("ReadFile() error =", err)
				}
				if string(got) != content {
					t.Errorf("unpacked content = %q, want %q", got, content)
				}
				return
			}
			got, err := os.ReadFile(filepath.Join(workDir, dirName))
			if err != nil {
				t.Fatal("ReadFile() error =", err)
			}
			if !bytes.Equal(got, gz) {
				t.Error("pushed content is not the original gzip")
			}
		})
	}
}

func TestStore_Dir_Push_LegacyAnnotations(t *testing.T) {
	const (
		dirName  = "testdir"
		fileName = "test.txt"
		content  = "hello world"
	)
	gz, tarDigest := packedDir(t, dirName, fileName, content)

	workDir := t.TempDir()
	s, err := New(workDir)
	if err != nil {
		t.Fatal("New() error =", err)
	}
	defer s.Close()

	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayerGzip,
		Digest:    digest.FromBytes(gz),
		Size:      int64(len(gz)),
		Annotations: map[string]string{
			ocispec.AnnotationTitle: dirName,
			AnnotationUnpackLegacy:  "true",
			AnnotationDigestLegacy:  tarDigest.String(),
		},
	}
	if err := s.Push(context.Background(), desc, bytes.NewReader(gz)); err != nil {
		t.Fatal("Store.Push() error =", err)
	}

	got, err := os.ReadFile(filepath.Join(workDir, dirName, fileName))
	if err != nil {
		t.Fatal("ReadFile() error =", err)
	}
	if string(got) != content {
		t.Errorf("unpacked content = %q, want %q", got, content)
	}
}

func TestStore_Dir_Push_LegacyDigestMismatch(t *testing.T) {
	const dirName = "testdir"
	gz, _ := packedDir(t, dirName, "test.txt", "hello world")

	workDir := t.TempDir()
	s, err := New(workDir)
	if err != nil {
		t.Fatal("New() error =", err)
	}
	defer s.Close()

	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayerGzip,
		Digest:    digest.FromBytes(gz),
		Size:      int64(len(gz)),
		Annotations: map[string]string{
			ocispec.AnnotationTitle: dirName,
			AnnotationUnpackLegacy:  "true",
			AnnotationDigestLegacy:  digest.FromString("tampered").String(),
		},
	}
	if err := s.Push(context.Background(), desc, bytes.NewReader(gz)); err == nil {
		t.Error("Store.Push() error = nil, want digest mismatch")
	}
}

func Test_resolveDirAnnotations(t *testing.T) {
	other := digest.FromString("other").String()

	tests := []struct {
		name        string
		annotations map[string]string
		wantUnpack  bool
		wantDigest  string
		wantErr     bool
	}{
		{name: "nil annotations"},
		{
			name:        "current keys",
			annotations: map[string]string{AnnotationUnpack: "true", AnnotationDigest: other},
			wantUnpack:  true,
			wantDigest:  other,
		},
		{
			name:        "legacy keys",
			annotations: map[string]string{AnnotationUnpackLegacy: "true", AnnotationDigestLegacy: other},
			wantUnpack:  true,
			wantDigest:  other,
		},
		{
			name: "both keys matching",
			annotations: map[string]string{
				AnnotationUnpack: "true", AnnotationDigest: other,
				AnnotationUnpackLegacy: "true", AnnotationDigestLegacy: other,
			},
			wantUnpack: true,
			wantDigest: other,
		},
		{
			name: "both digests conflicting",
			annotations: map[string]string{
				AnnotationUnpack: "true",
				AnnotationDigest: other, AnnotationDigestLegacy: digest.FromString("another").String(),
			},
			wantErr: true,
		},
		{
			name: "conflicting digests without unpack are ignored",
			annotations: map[string]string{
				AnnotationDigest:       "",
				AnnotationDigestLegacy: other,
			},
			wantErr: false,
		},
		{
			name: "empty current keys conflict with legacy keys",
			annotations: map[string]string{
				AnnotationUnpack: "", AnnotationDigest: "",
				AnnotationUnpackLegacy: "true", AnnotationDigestLegacy: other,
			},
			wantErr: true,
		},
		{
			name: "empty current unpack falls back to legacy",
			annotations: map[string]string{
				AnnotationUnpack:       "",
				AnnotationUnpackLegacy: "true",
			},
			wantUnpack: true,
		},
		{
			name: "conflicting current and legacy unpack flags",
			annotations: map[string]string{
				AnnotationUnpack:       "false",
				AnnotationUnpackLegacy: "true",
			},
			wantErr: true,
		},
		{
			name: "conflicting legacy and current unpack flags",
			annotations: map[string]string{
				AnnotationUnpack:       "true",
				AnnotationUnpackLegacy: "false",
			},
			wantErr: true,
		},
		{
			name:        "unpack flag false",
			annotations: map[string]string{AnnotationUnpack: "false"},
		},
		{
			name:        "legacy unpack flag false",
			annotations: map[string]string{AnnotationUnpackLegacy: "false"},
		},
		{
			name:        "unrelated values are not treated as true",
			annotations: map[string]string{AnnotationUnpack: "1", AnnotationUnpackLegacy: "TRUE"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveDirAnnotations(tt.annotations)
			if tt.wantErr {
				if err == nil {
					t.Fatal("resolveDirAnnotations() error = nil, wantErr = true")
				}
				return
			}
			if err != nil {
				t.Fatal("resolveDirAnnotations() error =", err)
			}
			if got.unpack != tt.wantUnpack {
				t.Errorf("unpack = %v, want %v", got.unpack, tt.wantUnpack)
			}
			if got.checksum != tt.wantDigest {
				t.Errorf("checksum = %q, want %q", got.checksum, tt.wantDigest)
			}
		})
	}
}
