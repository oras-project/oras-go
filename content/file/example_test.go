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
	if err := ioutil.WriteFile(path, content, 0444); err != nil {
		panic(err)
	}
	// prepare test file 2
	content = []byte("bar")
	filename = "bar.txt"
	path = filepath.Join(workingDir, filename)
	if err := ioutil.WriteFile(path, content, 0444); err != nil {
		panic(err)
	}

	// run tests
	exitCode := m.Run()

	// tear down and exit
	tearDown()
	os.Exit(exitCode)
}

// Example_packFile gives an example of adding files and packing a manifest
// referencing them.
func Example_packFiles() {
	store := file.New(workingDir)
	defer store.Close()
	ctx := context.Background()

	// 1. Add a file into the file store
	mediaType := "example/file"
	name1 := "foo.txt"
	fileDescriptor1, err := store.Add(ctx, name1, mediaType, "")
	if err != nil {
		panic(err)
	}
	fmt.Println("file1 descriptor:", fileDescriptor1)

	// 2. Add another file into the file store
	name2 := "bar.txt"
	fileDescriptor2, err := store.Add(ctx, name2, mediaType, "")
	if err != nil {
		panic(err)
	}
	fmt.Println("file2 descriptor:", fileDescriptor2)

	// 3. Generate a manifest referencing the files
	artifactType := "example/test"
	fileDescriptors := []ocispec.Descriptor{fileDescriptor1, fileDescriptor2}
	manifestDescriptor, err := oras.Pack(ctx, store, artifactType, fileDescriptors, oras.PackOptions{})
	if err != nil {
		panic(err)
	}
	fmt.Println("manifest media type:", manifestDescriptor.MediaType)

	// Output:
	// file1 descriptor: {example/file sha256:2c26b46b68ffc68ff99b453c1d30413413422d706483bfa0f98a5e886266e7ae 3 [] map[org.opencontainers.image.title:foo.txt] [] <nil> }
	// file2 descriptor: {example/file sha256:fcde2b2edba56bf408601fb721fe9b5c338d10ee429ea04fae5511b68fbf8fb9 3 [] map[org.opencontainers.image.title:bar.txt] [] <nil> }
	// manifest media type: application/vnd.oci.artifact.manifest.v1+json
}
