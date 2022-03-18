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

// Package file provides implementation of a content store based on file system.
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
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/internal/cas"
	"oras.land/oras-go/v2/internal/graph"
	"oras.land/oras-go/v2/internal/ioutil"
	"oras.land/oras-go/v2/internal/resolver"
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
	// AnnotationDigest is the annotation key for the digest of the uncompressed content.
	AnnotationDigest = "io.deis.oras.content.digest"
	// AnnotationUnpack is the annotation key for indication of unpacking.
	AnnotationUnpack = "io.deis.oras.content.unpack"
	// defaultBlobMediaType specifies the default blob media type.
	defaultBlobMediaType = ocispec.MediaTypeImageLayer
	// defaultBlobDirMediaType specifies the default blob directory media type.
	defaultBlobDirMediaType = ocispec.MediaTypeImageLayerGzip
	// defaultSizeLimit specifies the default size limit for pushing no-name contents.
	defaultSizeLimit = 1 << 22 // 4 MiB
)

// Store represents a file system based store, which implements `oras.Target`.
type Store struct {
	TarReproducible           bool
	AllowPathTraversalOnWrite bool
	DisableOverwrite          bool

	workingDir   string
	digestToPath sync.Map // map[digest.Digest]string
	nameToStatus sync.Map // map[string]chan struct{}
	tmpFiles     sync.Map // map[string]bool

	fallbackStorage content.Storage
	resolver        content.TagResolver
	graph           *graph.Memory
}

// New creates a file store, using a default limited memory storage
// as the fallback storage for contents without names.
// When pushing content without names, the size of content being pushed
// cannot exceed the default size limit: 4 MiB.
func New(workingDir string) *Store {
	return NewWithFallbackLimit(workingDir, defaultSizeLimit)
}

// NewWithFallbackLimit creates a file store, using a default
// limited memory storage as the fallback storage for contents without names.
// When pushing content without names, the size of content being pushed
// cannot exceed the size limit specified by the `limit` parameter.
func NewWithFallbackLimit(workingDir string, limit int64) *Store {
	m := cas.NewMemory()
	ls := content.LimitStorage(m, limit)
	return NewWithFallbackStorage(workingDir, ls)
}

// NewWithFallbackStorage creates a file store,
// using the provided fallback storage for contents without names.
func NewWithFallbackStorage(workingDir string, fallbackStorage content.Storage) *Store {
	return &Store{
		workingDir:      workingDir,
		fallbackStorage: fallbackStorage,
		resolver:        resolver.NewMemory(),
		graph:           graph.NewMemory(),
	}
}

// Close cleans up all the temp files used by the file store.
func (s *Store) Close() error {
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
// If name is not specified in the descriptor,
// the content will be fetched from the fallback storage.
func (s *Store) Fetch(ctx context.Context, target ocispec.Descriptor) (io.ReadCloser, error) {
	name := target.Annotations[ocispec.AnnotationTitle]
	if name == "" {
		return s.fallbackStorage.Fetch(ctx, target)
	}

	// check if the name is in the record
	status, exists := s.nameToStatus.Load(name)
	if !exists {
		return nil, fmt.Errorf("%s: %s: %w", name, target.MediaType, errdef.ErrNotFound)
	}

	done := status.(chan struct{})
	select {
	// if the work is in progress, wait for it to be done or cancaled.
	case <-done:
	case <-ctx.Done():
		return nil, errdef.ErrContextCanceled
	}

	val, ok := s.digestToPath.Load(target.Digest)
	if !ok {
		return nil, fmt.Errorf("%s: %s: %w", target.Digest, target.MediaType, errdef.ErrNotFound)
	}
	path := val.(string)

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
// If name is not specified in the descriptor,
// the content will be pushed to the fallback storage.
func (s *Store) Push(ctx context.Context, expected ocispec.Descriptor, content io.Reader) error {
	name := expected.Annotations[ocispec.AnnotationTitle]
	if name == "" {
		return s.fallbackStorage.Push(ctx, expected, content)
	}

	// check the status of the name
	status, committed := s.nameToStatus.LoadOrStore(name, make(chan struct{}))
	done := status.(chan struct{})
	if committed {
		// if the work is committed by other goroutines,
		// wait for the work to be done or canceled.
		select {
		case <-done:
			return fmt.Errorf("%s: %w", name, ErrDuplicateName)
		case <-ctx.Done():
			return errdef.ErrContextCanceled
		}
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

	// mark the work for the name as done.
	close(done)
	return s.graph.Index(ctx, s, expected)
}

// Exists returns true if the described content exists.
// If name is not specified in the descriptor,
// it will be the fallback storage to check if the content exists.
func (s *Store) Exists(ctx context.Context, target ocispec.Descriptor) (bool, error) {
	name := target.Annotations[ocispec.AnnotationTitle]
	if name == "" {
		return s.fallbackStorage.Exists(ctx, target)
	}

	// check the status of the name
	status, committed := s.nameToStatus.Load(name)
	if !committed {
		return false, nil
	}

	done := status.(chan struct{})
	// if the work is committed, wait for the work to be done or canceled.
	select {
	case <-done:
		return true, nil
	case <-ctx.Done():
		return false, errdef.ErrContextCanceled
	}
}

// Resolve resolves a reference to a descriptor.
func (s *Store) Resolve(ctx context.Context, ref string) (ocispec.Descriptor, error) {
	if ref == "" {
		return ocispec.Descriptor{}, errdef.ErrMissingReference
	}

	return s.resolver.Resolve(ctx, ref)
}

// Tag tags a descriptor with a reference string.
func (s *Store) Tag(ctx context.Context, desc ocispec.Descriptor, ref string) error {
	if ref == "" {
		return errdef.ErrMissingReference
	}

	exists, err := s.Exists(ctx, desc)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("%s: %s: %w", desc.Digest, desc.MediaType, errdef.ErrNotFound)
	}

	return s.resolver.Tag(ctx, desc, ref)
}

// UpEdges returns the nodes directly pointing to the current node.
// UpEdges returns nil without error if the node does not exists in the store.
func (s *Store) UpEdges(ctx context.Context, node ocispec.Descriptor) ([]ocispec.Descriptor, error) {
	return s.graph.UpEdges(ctx, node)
}

// Add adds a file into the file store.
func (s *Store) Add(ctx context.Context, name, mediaType, path string) (ocispec.Descriptor, error) {
	// check the status of the name
	status, committed := s.nameToStatus.LoadOrStore(name, make(chan struct{}))
	done := status.(chan struct{})
	if committed {
		// if the work is committed by other goroutines,
		// wait for the work to be done or canceled.
		select {
		case <-done:
			return ocispec.Descriptor{}, fmt.Errorf("%s: %w", name, ErrDuplicateName)
		case <-ctx.Done():
			return ocispec.Descriptor{}, errdef.ErrContextCanceled
		}
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

	// mark the work for the name as done
	close(done)
	return desc, nil
}

// PackFiles adds the given files as a pack into the file store,
// generates a manifest for the pack, and store the manifest in the file store.
// If succeeded, returns a descriptor of the manifest.
func (s *Store) PackFiles(ctx context.Context, names []string) (ocispec.Descriptor, error) {
	var layers []ocispec.Descriptor
	for _, name := range names {
		desc, err := s.Add(ctx, name, "", "")
		if err != nil {
			return ocispec.Descriptor{}, fmt.Errorf("failed to add %s: %w", name, err)
		}
		layers = append(layers, desc)
	}

	return oras.Pack(ctx, s, layers, oras.PackOptions{})
}

// saveFile saves content matching the descriptor to the given file.
func (s *Store) saveFile(fp *os.File, expected ocispec.Descriptor, content io.Reader) error {
	path := fp.Name()

	buf := bufPool.Get().(*[]byte)
	defer bufPool.Put(buf)
	if err := ioutil.CopyBuffer(fp, content, *buf, expected); err != nil {
		return fmt.Errorf("failed to copy content to %s: %w", path, err)
	}

	s.digestToPath.Store(expected.Digest, path)
	return nil
}

// pushFile saves content matching the descriptor to the target path.
func (s *Store) pushFile(target string, expected ocispec.Descriptor, content io.Reader) (err error) {
	if err := ensureDir(filepath.Dir(target)); err != nil {
		return fmt.Errorf("failed to ensure directories of the target path: %w", err)
	}

	fp, err := os.Create(target)
	if err != nil {
		return fmt.Errorf("failed to create file %s: %w", target, err)
	}
	defer func() {
		closeErr := fp.Close()
		if err == nil {
			err = closeErr
		}
	}()

	return s.saveFile(fp, expected, content)
}

// pushDir saves content matching the descriptor to the target directory.
func (s *Store) pushDir(name, target string, expected ocispec.Descriptor, content io.Reader) (err error) {
	if err := ensureDir(target); err != nil {
		return fmt.Errorf("failed to ensure directories of the target path: %w", err)
	}

	gz, err := s.tempFile()
	if err != nil {
		return err
	}
	defer func() {
		closeErr := gz.Close()
		if err == nil {
			err = closeErr
		}
	}()

	gzPath := gz.Name()
	// the digest of the gz is verified while saving
	if err := s.saveFile(gz, expected, content); err != nil {
		return fmt.Errorf("failed to save gzip to %s: %w", gzPath, err)
	}
	if err := gz.Sync(); err != nil {
		return fmt.Errorf("failed to flush gzip: %w", err)
	}

	checksum := expected.Annotations[AnnotationDigest]
	buf := bufPool.Get().(*[]byte)
	defer bufPool.Put(buf)
	if err := extractTarGzip(target, name, gzPath, checksum, *buf); err != nil {
		return fmt.Errorf("failed to extract tar to %s: %w", target, err)
	}
	return nil
}

// descriptorFromDir generates descriptor from the given directory.
func (s *Store) descriptorFromDir(name, mediaType, dir string) (desc ocispec.Descriptor, err error) {
	// make a temp file to store the gzip
	gz, err := s.tempFile()
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	defer func() {
		closeErr := gz.Close()
		if err == nil {
			err = closeErr
		}
	}()

	// compress the directory
	gzDigester := digest.Canonical.Digester()
	gzw := gzip.NewWriter(io.MultiWriter(gz, gzDigester.Hash()))
	defer func() {
		closeErr := gzw.Close()
		if err == nil {
			err = closeErr
		}
	}()

	tarDigester := digest.Canonical.Digester()
	tw := io.MultiWriter(gzw, tarDigester.Hash())
	buf := bufPool.Get().(*[]byte)
	defer bufPool.Put(buf)
	if err := tarDirectory(dir, name, tw, s.TarReproducible, *buf); err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("failed to tar %s: %w", dir, err)
	}

	// flush all
	if err := gzw.Close(); err != nil {
		return ocispec.Descriptor{}, err
	}
	if err := gz.Sync(); err != nil {
		return ocispec.Descriptor{}, err
	}

	fi, err := gz.Stat()
	if err != nil {
		return ocispec.Descriptor{}, err
	}

	// map gzip digest to gzip path
	gzDigest := gzDigester.Digest()
	s.digestToPath.Store(gzDigest, gz.Name())

	// generate descriptor
	if mediaType == "" {
		mediaType = defaultBlobDirMediaType
	}

	return ocispec.Descriptor{
		MediaType: mediaType,
		Digest:    gzDigest, // digest for the compressed content
		Size:      fi.Size(),
		Annotations: map[string]string{
			AnnotationDigest: tarDigester.Digest().String(), // digest fot the uncompressed content
			AnnotationUnpack: "true",                        // the content needs to be unpacked
		},
	}, nil
}

// descriptorFromFile generates descriptor from the given file.
func (s *Store) descriptorFromFile(fi os.FileInfo, mediaType, path string) (desc ocispec.Descriptor, err error) {
	fp, err := os.Open(path)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	defer func() {
		closeErr := fp.Close()
		if err == nil {
			err = closeErr
		}
	}()

	dgst, err := digest.FromReader(fp)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	// map digest to file path
	s.digestToPath.Store(dgst, path)

	// generate descriptor
	if mediaType == "" {
		mediaType = defaultBlobMediaType
	}

	return ocispec.Descriptor{
		MediaType: mediaType,
		Digest:    dgst,
		Size:      fi.Size(),
	}, nil
}

// resolveWritePath resolves the path to write for the given name.
func (s *Store) resolveWritePath(name string) (string, error) {
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

// tempFile creates a temp file with the file name format "oras_file_randomString",
// and returns the pointer to the temp file.
func (s *Store) tempFile() (*os.File, error) {
	tmp, err := os.CreateTemp("", "oras_file_*")
	if err != nil {
		return nil, err
	}

	s.tmpFiles.Store(tmp.Name(), true)
	return tmp, nil
}

// absPath returns the absolute path of the path.
func (s *Store) absPath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(s.workingDir, path)
}

// ensureDir ensures the directories of the path exists.
func ensureDir(path string) error {
	return os.MkdirAll(path, 0777)
}
