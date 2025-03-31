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

// Package file_test includes all the testable examples for the file package.
package file_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/file"
)

var workingDir string // the working directory for the examples

func TestMain(m *testing.M) {
	// prepare test directory
	var err error
	workingDir, err = os.MkdirTemp("", "oras_file_example_*")
	if err != nil {
		panic(err)
	}
	tearDown := func() {
		if err := os.RemoveAll(workingDir); err != nil {
			panic(err)
		}
	}

	// prepare test file 1
	content := []byte("foo")
	filename := "foo.txt"
	path := filepath.Join(workingDir, filename)
	if err := os.WriteFile(path, content, 0444); err != nil {
		panic(err)
	}
	// prepare test file 2
	content = []byte("bar")
	filename = "bar.txt"
	path = filepath.Join(workingDir, filename)
	if err := os.WriteFile(path, content, 0444); err != nil {
		panic(err)
	}

	// run tests
	exitCode := m.Run()

	// tear down and exit
	tearDown()
	os.Exit(exitCode)
}

// Example_packFiles gives an example of adding files and generating a manifest
// referencing the files as defined in image-spec v1.1.1.
func Example_packFiles() {
	store, err := file.New(workingDir)
	if err != nil {
		panic(err)
	}
	defer store.Close()
	ctx := context.Background()

	// 1. Add files into the file store
	mediaType := "example/file"
	fileNames := []string{"foo.txt", "bar.txt"}
	fileDescriptors := make([]ocispec.Descriptor, 0, len(fileNames))
	for _, name := range fileNames {
		fileDescriptor, err := store.Add(ctx, name, mediaType, "")
		if err != nil {
			panic(err)
		}
		fileDescriptors = append(fileDescriptors, fileDescriptor)

		fmt.Printf("file descriptor for %s: %v\n", name, fileDescriptor)
	}

	// 2. Generate a manifest referencing the files
	artifactType := "example/test"
	opts := oras.PackManifestOptions{
		Layers: fileDescriptors,
	}
	manifestDescriptor, err := oras.PackManifest(ctx, store, oras.PackManifestVersion1_1, artifactType, opts)
	if err != nil {
		panic(err)
	}
	fmt.Println("manifest media type:", manifestDescriptor.MediaType)

	// Output:
	// file descriptor for foo.txt: {example/file sha256:2c26b46b68ffc68ff99b453c1d30413413422d706483bfa0f98a5e886266e7ae 3 [] map[org.opencontainers.image.title:foo.txt] [] <nil> }
	// file descriptor for bar.txt: {example/file sha256:fcde2b2edba56bf408601fb721fe9b5c338d10ee429ea04fae5511b68fbf8fb9 3 [] map[org.opencontainers.image.title:bar.txt] [] <nil> }
	// manifest media type: application/vnd.oci.image.manifest.v1+json
}
