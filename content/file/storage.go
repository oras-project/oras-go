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
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	artifactspec "github.com/oras-project/artifacts-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/internal/cas"
	"oras.land/oras-go/v2/internal/docker"
	"oras.land/oras-go/v2/internal/fileutil"
	"oras.land/oras-go/v2/internal/ioutil"
	"oras.land/oras-go/v2/internal/lockutil"
)

// bufPool is a pool of byte buffers that can be reused for copying content
// between files.
var bufPool = sync.Pool{
	New: func() interface{} {
		// the buffer size should be larger than or equal to 128 KiB
		// for performance considerations.
		// we choose 1 MiB here so there will be less disk I/O.
		buffer := make([]byte, 1<<20) // buffer size = 1 MiB
		return &buffer
	},
}

const (
	// AnnotationDigest is the annotation key for the digest of the uncompressed content
	AnnotationDigest = "io.deis.oras.content.digest"
	// AnnotationUnpack is the annotation key for indication of unpacking
	AnnotationUnpack = "io.deis.oras.content.unpack"
	// defaultBlobMediaType specifies the default blob media type
	defaultBlobMediaType = ocispec.MediaTypeImageLayer
	// defaultBlobDirMediaType specifies the default blob directory media type
	defaultBlobDirMediaType = ocispec.MediaTypeImageLayerGzip
)

// storage implements `content.Storage`
type storage struct {
	Reproducible              bool
	IgnoreNoName              bool
	AllowPathTraversalOnWrite bool
	DisableOverwrite          bool

	workingDir string
	memoryCAS  *cas.Memory
	locker     *lockutil.ReferenceLocker
	dgstToPath sync.Map // map[digest.Digest]string
	nameToPath sync.Map // map[string]string
	tmpFiles   sync.Map // map[string]bool
}

type FileRef struct {
	name      string
	mediaType string
	path      string
}

func newStorage(workingDir string) *storage {
	return &storage{
		workingDir: workingDir,
		memoryCAS:  cas.NewMemory(),
		locker:     lockutil.New(),
	}
}

func (s *storage) Close() error {
	s.locker.Close()

	var errs []string
	s.tmpFiles.Range(func(name, _ interface{}) bool {
		if err := os.Remove(name.(string)); err != nil {
			errs = append(errs, err.Error())
		}
		return true
	})
	return errors.New(strings.Join(errs, "; "))
}

// Fetch fetches the content identified by the descriptor.
func (s *storage) Fetch(ctx context.Context, target ocispec.Descriptor) (io.ReadCloser, error) {
	if yes := isManifest(target); yes {
		return s.memoryCAS.Fetch(ctx, target)
	}

	// make sure content exists
	val, exists := s.dgstToPath.Load(target.Digest)
	if !exists {
		return nil, fmt.Errorf("%s: %s: %w", target.Digest, target.MediaType, errdef.ErrNotFound)
	}
	path := val.(string)

	// make sure name exists
	name := target.Annotations[ocispec.AnnotationTitle]
	if name != "" {
		_, exists := s.nameToPath.Load(name)
		if !exists {
			return nil, fmt.Errorf("%s: %s: %w", name, target.MediaType, errdef.ErrNotFound)
		}
	}

	fp, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%s: %s: %w", target.Digest, target.MediaType, errdef.ErrNotFound)
		}
		return nil, err
	}

	return fp, nil
}

// Push pushes the content, matching the expected descriptor.
func (s *storage) Push(ctx context.Context, expected ocispec.Descriptor, content io.Reader) error {
	if yes := isManifest(expected); yes {
		return s.memoryCAS.Push(ctx, expected, content)
	}

	name := expected.Annotations[ocispec.AnnotationTitle]
	if name == "" {
		// if we were not told to ignore NoName, then return an error
		if !s.IgnoreNoName {
			return ErrNoName
		}

		_, exists := s.dgstToPath.Load(expected.Digest)
		if exists {
			return fmt.Errorf("%s: %s: %w", expected.Digest, expected.MediaType, errdef.ErrAlreadyExists)
		}

		tmp, err := s.tempFile()
		if err != nil {
			return fmt.Errorf("failed to get temp file: %w", err)
		}
		defer tmp.Close()
		return s.saveFile(tmp, expected, content)
	}

	// check if name already exists
	_, exists := s.nameToPath.Load(name)
	if exists {
		return fmt.Errorf("%s: %w", name, ErrDuplicateName)
	}

	// only one go-routine is allowed to write file
	s.locker.Lock(name)
	defer s.locker.Unlock(name)

	// check if name already exists
	_, exists = s.nameToPath.Load(name)
	if exists {
		return fmt.Errorf("%s: %w", name, ErrDuplicateName)
	}

	target, err := s.resolveWritePath(name)
	if err != nil {
		return fmt.Errorf("failed to resolve path for writing: %w", err)
	}

	if needUnpack := expected.Annotations[AnnotationUnpack]; needUnpack == "true" {
		err = s.pushDir(name, target, expected, content)
	} else {
		err = s.pushFile(target, expected, content)
	}
	if err != nil {
		return err
	}

	s.nameToPath.Store(name, target)
	return nil
}

// Exists returns true if the described content exists.
func (s *storage) Exists(ctx context.Context, target ocispec.Descriptor) (bool, error) {
	if yes := isManifest(target); yes {
		return s.memoryCAS.Exists(ctx, target)
	}

	// check if content exists
	val, exists := s.dgstToPath.Load(target.Digest)
	if !exists {
		return false, nil
	}
	path := val.(string)

	// check if name exists
	name := target.Annotations[ocispec.AnnotationTitle]
	if name != "" {
		val, exists := s.nameToPath.Load(name)
		if !exists {
			return false, nil
		}
		path = val.(string)
	}

	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}

	if os.IsNotExist(err) {
		return false, nil
	}

	return false, err
}

func (s *storage) Add(name, mediaType, path string) (ocispec.Descriptor, error) {
	// check if name already exists
	_, exists := s.nameToPath.Load(name)
	if exists {
		return ocispec.Descriptor{}, fmt.Errorf("%s: %w", name, ErrDuplicateName)
	}

	if path == "" {
		path = name
	}
	path = s.absPath(path)

	fi, err := os.Stat(path)
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("failed to stat %s: %w", path, err)
	}

	var desc ocispec.Descriptor
	if fi.IsDir() {
		desc, err = s.descriptorFromDir(name, mediaType, path)
	} else {
		desc, err = s.descriptorFromFile(fi, mediaType, path)
	}
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("failed to generate descriptor from %s: %w", path, err)
	}

	if desc.Annotations == nil {
		desc.Annotations = make(map[string]string)
	}
	desc.Annotations[ocispec.AnnotationTitle] = name

	// map name to extracted path
	s.nameToPath.Store(name, path)
	return desc, nil
}

func (s *storage) Pack(ctx context.Context, files []FileRef, opts content.PackOpts, manifestName, configName string) (ocispec.Descriptor, error) {
	var layers []ocispec.Descriptor
	for _, file := range files {
		desc, err := s.Add(file.name, file.mediaType, file.path)
		if err != nil {
			return ocispec.Descriptor{}, err
		}
		layers = append(layers, desc)
	}

	if manifestName != "" {
		if opts.ManifestAnnotations == nil {
			opts.ManifestAnnotations = make(map[string]string)
		}
		opts.ManifestAnnotations[ocispec.AnnotationTitle] = manifestName
	}

	if configName != "" {
		if opts.ConfigAnnotations == nil {
			opts.ConfigAnnotations = make(map[string]string)
		}
		opts.ConfigAnnotations[ocispec.AnnotationTitle] = configName
	}

	return content.Pack(ctx, s, layers, opts)
}

func (s *storage) saveFile(fp *os.File, expected ocispec.Descriptor, content io.Reader) error {
	path := fp.Name()

	buf := bufPool.Get().(*[]byte)
	defer bufPool.Put(buf)
	if err := ioutil.CopyBuffer(fp, content, *buf, expected); err != nil {
		return fmt.Errorf("failed to copy content to %s: %w", path, err)
	}

	s.dgstToPath.Store(expected.Digest, path)
	return nil
}

func (s *storage) pushFile(target string, expected ocispec.Descriptor, content io.Reader) error {
	if err := ensureDir(filepath.Dir(target)); err != nil {
		return fmt.Errorf("failed to ensure directories of the target path: %w", err)
	}

	fp, err := os.Create(target)
	if err != nil {
		return fmt.Errorf("failed to create file %s: %w", target, err)
	}
	defer fp.Close()

	return s.saveFile(fp, expected, content)
}

func (s *storage) pushDir(name, target string, expected ocispec.Descriptor, content io.Reader) error {
	if err := ensureDir(target); err != nil {
		return fmt.Errorf("failed to ensure directories of the target path: %w", err)
	}

	zip, err := s.tempFile()
	if err != nil {
		return err
	}
	zipPath := zip.Name()
	if err := s.saveFile(zip, expected, content); err != nil {
		return fmt.Errorf("failed to save zip to %s: %w", zipPath, err)
	}
	zip.Close()

	checksum := expected.Annotations[AnnotationDigest]
	buf := bufPool.Get().(*[]byte)
	defer bufPool.Put(buf)
	if err := fileutil.ExtractTarGzip(target, name, zipPath, checksum, *buf); err != nil {
		return fmt.Errorf("failed to extract tar to %s: %w", target, err)
	}
	return nil
}

func (s *storage) descriptorFromDir(name, mediaType, dir string) (ocispec.Descriptor, error) {
	// make a temp file for the zip
	zip, err := s.tempFile()
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	defer zip.Close()

	// compress directory
	digester := digest.Canonical.Digester()
	zw := gzip.NewWriter(io.MultiWriter(zip, digester.Hash()))
	defer zw.Close()

	tarDigester := digest.Canonical.Digester()
	buf := bufPool.Get().(*[]byte)
	defer bufPool.Put(buf)
	if err := fileutil.TarDirectory(dir, name, io.MultiWriter(zw, tarDigester.Hash()), s.Reproducible, *buf); err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("failed to tar %s: %w", dir, err)
	}

	// flush all
	if err := zw.Close(); err != nil {
		return ocispec.Descriptor{}, err
	}
	if err := zip.Sync(); err != nil {
		return ocispec.Descriptor{}, err
	}

	fi, err := zip.Stat()
	if err != nil {
		return ocispec.Descriptor{}, err
	}

	// map zip digest to zip path
	zipDgst := digester.Digest()
	s.dgstToPath.Store(zipDgst, zip.Name())

	// generate descriptor
	if mediaType == "" {
		mediaType = defaultBlobDirMediaType
	}

	return ocispec.Descriptor{
		MediaType: mediaType,
		Digest:    digester.Digest(), // digest for the compressed content
		Size:      fi.Size(),
		Annotations: map[string]string{
			AnnotationDigest: tarDigester.Digest().String(), // digest fot the uncompressed content
			AnnotationUnpack: "true",                        // the content needs to be unpacked
		},
	}, nil
}

func (s *storage) descriptorFromFile(fi os.FileInfo, mediaType, path string) (ocispec.Descriptor, error) {
	fp, err := os.Open(path)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	defer fp.Close()

	dgst, err := digest.FromReader(fp)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	// map digest to path
	s.dgstToPath.Store(dgst, path)

	if mediaType == "" {
		mediaType = defaultBlobMediaType
	}

	return ocispec.Descriptor{
		MediaType: mediaType,
		Digest:    dgst,
		Size:      fi.Size(),
	}, nil
}

func (s *storage) resolveWritePath(name string) (string, error) {
	path := s.absPath(name)
	if !s.AllowPathTraversalOnWrite {
		base, err := filepath.Abs(s.workingDir)
		if err != nil {
			return "", err
		}
		target, err := filepath.Abs(path)
		if err != nil {
			return "", err
		}
		rel, err := filepath.Rel(base, target)
		if err != nil {
			return "", ErrPathTraversalDisallowed
		}
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, "../") || rel == ".." {
			return "", ErrPathTraversalDisallowed
		}
	}
	if s.DisableOverwrite {
		if _, err := os.Stat(path); err == nil {
			return "", ErrOverwriteDisallowed
		} else if !os.IsNotExist(err) {
			return "", err
		}
	}
	return path, nil
}

func (s *storage) tempFile() (*os.File, error) {
	tmp, err := os.CreateTemp("", "oras_file_*")
	if err != nil {
		return nil, err
	}

	s.tmpFiles.Store(tmp.Name(), true)
	return tmp, nil
}

func (s *storage) absPath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(s.workingDir, path)
}

func isManifest(desc ocispec.Descriptor) bool {
	switch desc.MediaType {
	case docker.MediaTypeManifest,
		docker.MediaTypeManifestList,
		ocispec.MediaTypeImageManifest,
		ocispec.MediaTypeImageIndex,
		artifactspec.MediaTypeArtifactManifest:
		return true
	default:
		return false
	}
}

// ensureDir ensures the directories of the path exists.
func ensureDir(path string) error {
	return os.MkdirAll(path, 0777)
}
