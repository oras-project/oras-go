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

package file_test

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/file"
)

var workingDir string

func TestMain(m *testing.M) {
	// prepare test content
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

	content := []byte("test")
	filename := "test.txt"
	path := filepath.Join(workingDir, filename)
	if err := ioutil.WriteFile(path, content, 0444); err != nil {
		panic(err)
	}

	// run tests
	exitCode := m.Run()

	// tear down and exit
	tearDown()
	os.Exit(exitCode)
}

// ExampleVerifyReader gives an example of adding a single file and packing a
// manifest referencing it.
func Example_packFile() {
	store := file.New(workingDir)
	defer store.Close()
	ctx := context.Background()

	// 1. Add the file into the file store
	mediaType := "example/file"
	fileDescriptor, err := store.Add(ctx, "test.txt", mediaType, "")
	if err != nil {
		panic(err)
	}
	fmt.Println("file descriptor:", fileDescriptor)

	// 2. Generate a manifest referencing the file
	artifactType := "example/test"
	fileDescriptors := []ocispec.Descriptor{fileDescriptor}
	manifestDescriptor, err := oras.Pack(ctx, store, artifactType, fileDescriptors, oras.PackOptions{})
	if err != nil {
		panic(err)
	}
	fmt.Println("manifest media type:", manifestDescriptor.MediaType)

	// Output:
	// file descriptor: {example/file sha256:9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08 4 [] map[org.opencontainers.image.title:test.txt] [] <nil> }
	// manifest media type: application/vnd.oci.artifact.manifest.v1+json
}
