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

package content

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"github.com/containerd/containerd/remotes"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/suite"
)

type ContentTestSuite struct {
	suite.Suite
	TestMemoryStore *Memory
	TestFileStore   *File
}

var (
	testDirRoot, _ = filepath.Abs("../../.test")
	testFileName   = filepath.Join(testDirRoot, "testfile")
	testRef        = "abc123"
	testContent    = []byte("Hello World!")
	testDescriptor = ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageConfig,
		Digest:    digest.FromBytes(testContent),
		Size:      int64(len(testContent)),
		Annotations: map[string]string{
			ocispec.AnnotationTitle: testRef,
		},
	}
	testBadContent    = []byte("doesnotexist")
	testBadDescriptor = ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageConfig,
		Digest:    digest.FromBytes(testBadContent),
		Size:      int64(len(testBadContent)),
	}
)

func (suite *ContentTestSuite) SetupSuite() {
	testMemoryStore := NewMemory()
	desc, err := testMemoryStore.Add(testRef, "", testContent)
	suite.Nil(err, "no error adding testContent to memory store")
	_, err = testMemoryStore.GenerateManifest(testRef, nil, desc)
	suite.Nil(err, "no error adding ref to memory store")
	suite.TestMemoryStore = testMemoryStore

	os.Remove(testFileName)
	err = ioutil.WriteFile(testFileName, testContent, 0644)
	suite.Nil(err, "no error creating test file on disk")
	testFileStore := NewFile(testDirRoot, WithErrorOnNoName())
	desc, err = testFileStore.Add(testRef, "", testFileName)
	suite.Nil(err, "no error adding item to file store")
	_, err = testFileStore.GenerateManifest(testRef, nil, desc)
	suite.Nil(err, "no error adding ref to file store")
	suite.TestFileStore = testFileStore
}

// Tests all Writers (Ingesters)
func (suite *ContentTestSuite) Test_0_Ingesters() {

	ctx := context.Background()
	memPusher, _ := suite.TestMemoryStore.Pusher(ctx, "")
	filePusher, _ := suite.TestFileStore.Pusher(ctx, "")
	ingesters := map[string]remotes.Pusher{
		"memory": memPusher,
		"file":   filePusher,
	}

	for key, ingester := range ingesters {

		// Bad ref
		writer, err := ingester.Push(ctx, testBadDescriptor)
		if key == "file" {
			suite.NotNil(err, fmt.Sprintf("no error getting writer w bad ref for %s store", key))
		}

		// Good ref
		ctx = context.Background()
		writer, err = ingester.Push(ctx, testDescriptor)
		suite.Nil(err, fmt.Sprintf("no error getting writer w good ref for %s store", key))
		_, err = writer.Write(testContent)
		suite.Nil(err, fmt.Sprintf("no error using writer.Write w good ref for %s store", key))
		err = writer.Commit(ctx, testDescriptor.Size, testDescriptor.Digest)
		suite.Nil(err, fmt.Sprintf("no error using writer.Commit w good ref for %s store", key))

		digest := writer.Digest()
		suite.Equal(testDescriptor.Digest, digest, fmt.Sprintf("correct digest for %s store", key))
		status, err := writer.Status()
		suite.Nil(err, fmt.Sprintf("no error retrieving writer status for %s store", key))
		suite.Equal(testRef, status.Ref, fmt.Sprintf("correct status for %s store", key))

		// close writer
		err = writer.Close()
		suite.Nil(err, fmt.Sprintf("no error closing writer w bad ref for %s store", key))
		err = writer.Commit(ctx, testDescriptor.Size, testDescriptor.Digest)
		suite.NotNil(err, fmt.Sprintf("error using writer.Commit when closed w good ref for %s store", key))

		// re-init writer after closing
		writer, _ = ingester.Push(ctx, testDescriptor)
		writer.Write(testContent)

		// invalid truncate size
		err = writer.Truncate(123456789)
		suite.NotNil(err, fmt.Sprintf("error using writer.Truncate w invalid size, good ref for %s store", key))

		// valid truncate size
		err = writer.Truncate(0)
		suite.Nil(err, fmt.Sprintf("no error using writer.Truncate w valid size, good ref for %s store", key))

		writer.Commit(ctx, testDescriptor.Size, testDescriptor.Digest)

		// bad size
		err = writer.Commit(ctx, 1, testDescriptor.Digest)
		fmt.Println(err)
		suite.NotNil(err, fmt.Sprintf("error using writer.Commit w bad size, good ref for %s store", key))

		// bad digest
		err = writer.Commit(ctx, 0, testBadDescriptor.Digest)
		suite.NotNil(err, fmt.Sprintf("error using writer.Commit w bad digest, good ref for %s store", key))
	}
}

// Tests all Readers (Providers)
func (suite *ContentTestSuite) Test_1_Providers() {
	ctx := context.Background()
	memFetcher, _ := suite.TestMemoryStore.Fetcher(ctx, testRef)
	fileFetcher, _ := suite.TestFileStore.Fetcher(ctx, testRef)
	providers := map[string]remotes.Fetcher{
		"memory": memFetcher,
		"file":   fileFetcher,
	}

	// Readers (Providers)
	for key, provider := range providers {

		// Bad ref
		ctx := context.Background()
		_, err := provider.Fetch(ctx, testBadDescriptor)
		suite.NotNil(err, fmt.Sprintf("error with bad ref for %s store", key))

		// Good ref
		ctx = context.Background()
		reader, err := provider.Fetch(ctx, testDescriptor)
		suite.Nil(err, fmt.Sprintf("no error with good ref for %s store", key))

		// readerat Close()
		err = reader.Close()
		suite.Nil(err, fmt.Sprintf("no error closing reader for %s store", key))

		// file missing
		if key == "file" {
			os.Remove(testFileName)
			ctx := context.Background()
			_, err := provider.Fetch(ctx, testDescriptor)
			suite.NotNil(err, fmt.Sprintf("error with good ref for %s store (file missing)", key))
		}
	}
}

func (suite *ContentTestSuite) Test_2_GetByName() {
	// NotFound
	_, _, ok := suite.TestMemoryStore.GetByName("doesnotexist")
	suite.False(ok, "unable to find non-existant ref by name for memory store")

	// Found
	_, _, ok = suite.TestMemoryStore.GetByName(testRef)
	suite.True(ok, "able to find non-existant ref by name for memory store")
}

func TestContentTestSuite(t *testing.T) {
	suite.Run(t, new(ContentTestSuite))
}
