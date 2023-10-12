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

package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"reflect"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/internal/cas"
	"oras.land/oras-go/v2/internal/descriptor"
)

// A(manifest)-------------------+
//
//	|                         |
//	|                         |
//	|                         |
//	v                         |
//
// B(manifest)-----------------+ |
//
//	|                       | |
//	|                       | |
//	v                       v v
//
// C(layer)                  D(layer)
func TestDeletableMemory_IndexAndRemove(t *testing.T) {
	testFetcher := cas.NewMemory()
	testMemory := NewMemory()
	ctx := context.Background()

	// generate test content
	var blobs [][]byte
	var descs []ocispec.Descriptor
	appendBlob := func(mediaType string, blob []byte) ocispec.Descriptor {
		blobs = append(blobs, blob)
		descs = append(descs, ocispec.Descriptor{
			MediaType: mediaType,
			Digest:    digest.FromBytes(blob),
			Size:      int64(len(blob)),
		})
		return descs[len(descs)-1]
	}
	generateManifest := func(layers ...ocispec.Descriptor) ocispec.Descriptor {
		manifest := ocispec.Manifest{
			Config: ocispec.Descriptor{MediaType: "test config"},
			Layers: layers,
		}
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		return appendBlob(ocispec.MediaTypeImageManifest, manifestJSON)
	}
	descC := appendBlob("layer node C", []byte("Node C is a layer")) // blobs[0], layer "C"
	descD := appendBlob("layer node D", []byte("Node D is a layer")) // blobs[1], layer "D"
	descB := generateManifest(descs[0:2]...)                         // blobs[2], manifest "B"
	descA := generateManifest(descs[1:3]...)                         // blobs[3], manifest "A"

	// prepare the content in the fetcher, so that it can be used to test Index.
	testContents := []ocispec.Descriptor{descC, descD, descB, descA}
	for i := 0; i < len(blobs); i++ {
		testFetcher.Push(ctx, testContents[i], bytes.NewReader(blobs[i]))
	}

	// make sure that testFetcher works
	rc, err := testFetcher.Fetch(ctx, descA)
	if err != nil {
		t.Errorf("testFetcher.Fetch() error = %v", err)
	}
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Errorf("testFetcher.Fetch().Read() error = %v", err)
	}
	err = rc.Close()
	if err != nil {
		t.Errorf("testFetcher.Fetch().Close() error = %v", err)
	}
	if !bytes.Equal(got, blobs[3]) {
		t.Errorf("testFetcher.Fetch() = %v, want %v", got, blobs[4])
	}

	nodeKeyA := descriptor.FromOCI(descA)
	nodeKeyB := descriptor.FromOCI(descB)
	nodeKeyC := descriptor.FromOCI(descC)
	nodeKeyD := descriptor.FromOCI(descD)

	// index and check the information of node D
	testMemory.Index(ctx, testFetcher, descD)
	successorsD, exist := testMemory.successors[nodeKeyD]
	if !exist {
		t.Errorf("successor entry of %s should exist", "D")
	}
	if successorsD == nil {
		t.Errorf("successors of %s should not be nil", "D")
	}
	for range successorsD {
		t.Errorf("successors of %s should be empty", "C")
	}
	// there should be no entry of D in testMemory.predecessors yet.
	_, exist = testMemory.predecessors[nodeKeyD]
	if exist {
		t.Errorf("predecessor entry of %s should not exist yet", "D")
	}

	// index and check the information of node C
	testMemory.Index(ctx, testFetcher, descC)
	successorsC, exist := testMemory.successors[nodeKeyC]
	if !exist {
		t.Errorf("successor entry of %s should exist", "C")
	}
	if successorsC == nil {
		t.Errorf("successors of %s should not be nil", "C")
	}
	for range successorsC {
		t.Errorf("successors of %s should be empty", "C")
	}
	// there should be no entry of C in testMemory.predecessors yet.
	_, exist = testMemory.predecessors[nodeKeyC]
	if exist {
		t.Errorf("predecessor entry of %s should not exist yet", "C")
	}

	// index and check the information of node A
	testMemory.Index(ctx, testFetcher, descA)
	successorsA := testMemory.successors[nodeKeyA]
	if !successorsA.Contains(nodeKeyB) {
		t.Errorf("successors of %s should contain %s", "A", "B")
	}
	if !successorsA.Contains(nodeKeyD) {
		t.Errorf("successors of %s should contain %s", "A", "D")
	}
	// there should be no entry of A in testMemory.predecessors.
	_, exist = testMemory.predecessors[nodeKeyA]
	if exist {
		t.Errorf("predecessor entry of %s should not exist", "A")
	}
	// there should be an entry of D in testMemory.predecessors by now
	// and it should contain A but not B.
	predecessorsD, exist := testMemory.predecessors[nodeKeyD]
	if !exist {
		t.Errorf("predecessor entry of %s should exist by now", "D")
	}
	if !predecessorsD.Contains(nodeKeyA) {
		t.Errorf("predecessors of %s should contain %s", "D", "A")
	}
	if predecessorsD.Contains(nodeKeyB) {
		t.Errorf("predecessors of %s should not contain %s yet", "D", "B")
	}
	// there should be an entry of B in testMemory.predecessors now
	// and it should contain A.
	predecessorsB, exist := testMemory.predecessors[nodeKeyB]
	if !exist {
		t.Errorf("predecessor entry of %s should exist by now", "B")
	}
	if !predecessorsB.Contains(nodeKeyA) {
		t.Errorf("predecessors of %s should contain %s", "B", "A")
	}

	// index and check the information of node B
	testMemory.Index(ctx, testFetcher, descB)
	successorsB := testMemory.successors[nodeKeyB]
	if !successorsB.Contains(nodeKeyC) {
		t.Errorf("successors of %s should contain %s", "B", "C")
	}
	if !successorsB.Contains(nodeKeyD) {
		t.Errorf("successors of %s should contain %s", "B", "D")
	}
	// there should be an entry of C in testMemory.predecessors by now
	// and it should contain B.
	predecessorsC, exist := testMemory.predecessors[nodeKeyC]
	if !exist {
		t.Errorf("predecessor entry of %s should exist by now", "C")
	}
	if !predecessorsC.Contains(nodeKeyB) {
		t.Errorf("predecessors of %s should contain %s", "C", "B")
	}
	// predecessors of D should have been updated now.
	if !predecessorsD.Contains(nodeKeyB) {
		t.Errorf("predecessors of %s should contain %s", "D", "B")
	}
	if !predecessorsD.Contains(nodeKeyA) {
		t.Errorf("predecessors of %s should contain %s", "D", "A")
	}

	// remove node B and check the stored information
	testMemory.Remove(ctx, descB)
	// verify B' predecessors info: B's entry in testMemory.predecessors should
	// still exist, since its predecessor A still exists
	predecessorsB, exists := testMemory.predecessors[nodeKeyB]
	if !exists {
		t.Errorf("testDeletableMemory.predecessors should still contain the entry of %v", "B")
	}
	if !predecessorsB.Contains(nodeKeyA) {
		t.Errorf("predecessors of %s should still contain %s", "B", "A")
	}
	// verify B' successors info: B's entry in testMemory.successors should no
	// longer exist
	if _, exists := testMemory.successors[nodeKeyB]; exists {
		t.Errorf("testDeletableMemory.successors should not contain the entry of %v", "B")
	}
	// verify B' predecessors' successors info: B should still exist in A's
	// successors
	if !successorsA.Contains(nodeKeyB) {
		t.Errorf("successors of %s should still contain %s", "A", "B")
	}
	// verify B' successors' predecessors info: C's entry in testMemory.predecessors
	// should no longer exist, since C's only predecessor B is already deleted.
	// B should no longer exist in D's predecessors.
	if _, exists = testMemory.predecessors[nodeKeyC]; exists {
		t.Errorf("predecessor entry of %s should no longer exist by now, since all its predecessors have been deleted", "C")
	}
	if predecessorsD.Contains(nodeKeyB) {
		t.Errorf("predecessors of %s should not contain %s", "D", "B")
	}

	// remove node A and check the stored information
	testMemory.Remove(ctx, descA)
	// verify A' successors info: A's entry in testMemory.successors should no
	// longer exist
	if _, exists := testMemory.successors[nodeKeyA]; exists {
		t.Errorf("testDeletableMemory.successors should not contain the entry of %v", "A")
	}
	// verify A' successors' predecessors info: D's entry in testMemory.predecessors
	// should no longer exist, since all predecessors of D are already deleted.
	// B's entry in testMemory.predecessors should no longer exist, since B's only
	// predecessor A is already deleted.
	if _, exists = testMemory.predecessors[nodeKeyD]; exists {
		t.Errorf("predecessor entry of %s should no longer exist by now, since all its predecessors have been deleted", "D")
	}
	if _, exists = testMemory.predecessors[nodeKeyC]; exists {
		t.Errorf("predecessor entry of %s should no longer exist by now, since all its predecessors have been deleted", "C")
	}
}

/*
+---------A(index)--------+
|             |           |
|             |           |
|             |           |
|             |           |
v             v           v
B(manifest)   C(manifest) D (manifest)
|             |           |
|             |           |
|             |           v
+-->E(layer)<-+------->F(layer)
*/
func TestDeletableMemory_IndexAllAndPredecessors(t *testing.T) {
	testFetcher := cas.NewMemory()
	testDeletableMemory := NewMemory()
	ctx := context.Background()

	// generate test content
	var blobs [][]byte
	var descriptors []ocispec.Descriptor
	appendBlob := func(mediaType string, blob []byte) ocispec.Descriptor {
		blobs = append(blobs, blob)
		descriptors = append(descriptors, ocispec.Descriptor{
			MediaType: mediaType,
			Digest:    digest.FromBytes(blob),
			Size:      int64(len(blob)),
		})
		return descriptors[len(descriptors)-1]
	}
	generateManifest := func(layers ...ocispec.Descriptor) ocispec.Descriptor {
		manifest := ocispec.Manifest{
			Layers: layers,
		}
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		return appendBlob(ocispec.MediaTypeImageManifest, manifestJSON)
	}
	generateIndex := func(manifests ...ocispec.Descriptor) ocispec.Descriptor {
		index := ocispec.Index{
			Manifests: manifests,
		}
		indexJSON, err := json.Marshal(index)
		if err != nil {
			t.Fatal(err)
		}
		return appendBlob(ocispec.MediaTypeImageIndex, indexJSON)
	}
	descE := appendBlob("layer node E", []byte("Node E is a layer")) // blobs[0], layer "E"
	descF := appendBlob("layer node F", []byte("Node F is a layer")) // blobs[1], layer "F"
	descB := generateManifest(descriptors[0:1]...)                   // blobs[2], manifest "B"
	descC := generateManifest(descriptors[0:2]...)                   // blobs[3], manifest "C"
	descD := generateManifest(descriptors[1:2]...)                   // blobs[4], manifest "D"
	descA := generateIndex(descriptors[2:5]...)                      // blobs[5], index "A"
	testFetcher.Push(ctx, descA, bytes.NewReader(blobs[5]))
	testFetcher.Push(ctx, descB, bytes.NewReader(blobs[2]))
	testFetcher.Push(ctx, descC, bytes.NewReader(blobs[3]))
	testFetcher.Push(ctx, descD, bytes.NewReader(blobs[4]))
	testFetcher.Push(ctx, descE, bytes.NewReader(blobs[0]))
	testFetcher.Push(ctx, descF, bytes.NewReader(blobs[1]))

	// make sure that testFetcher works
	rc, err := testFetcher.Fetch(ctx, descA)
	if err != nil {
		t.Errorf("testFetcher.Fetch() error = %v", err)
	}
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Errorf("testFetcher.Fetch().Read() error = %v", err)
	}
	err = rc.Close()
	if err != nil {
		t.Errorf("testFetcher.Fetch().Close() error = %v", err)
	}
	if !bytes.Equal(got, blobs[5]) {
		t.Errorf("testFetcher.Fetch() = %v, want %v", got, blobs[4])
	}

	// index node A into graph.DeletableMemory using IndexAll
	testDeletableMemory.IndexAll(ctx, testFetcher, descA)

	// check that the content of testDeletableMemory.predecessors and successors
	// are correct
	nodeKeyA := descriptor.FromOCI(descA)
	nodeKeyB := descriptor.FromOCI(descB)
	nodeKeyC := descriptor.FromOCI(descC)
	nodeKeyD := descriptor.FromOCI(descD)
	nodeKeyE := descriptor.FromOCI(descE)
	nodeKeyF := descriptor.FromOCI(descF)

	// check the information of node A
	predecessorsA := testDeletableMemory.predecessors[nodeKeyA]
	successorsA := testDeletableMemory.successors[nodeKeyA]
	for range predecessorsA {
		t.Errorf("predecessors of %s should be empty", "A")
	}
	if !successorsA.Contains(nodeKeyB) {
		t.Errorf("successors of %s should contain %s", "A", "B")
	}
	if !successorsA.Contains(nodeKeyC) {
		t.Errorf("successors of %s should contain %s", "A", "C")
	}
	if !successorsA.Contains(nodeKeyD) {
		t.Errorf("successors of %s should contain %s", "A", "D")
	}

	// check the information of node B
	predecessorsB := testDeletableMemory.predecessors[nodeKeyB]
	successorsB := testDeletableMemory.successors[nodeKeyB]
	if !predecessorsB.Contains(nodeKeyA) {
		t.Errorf("predecessors of %s should contain %s", "B", "A")
	}
	if !successorsB.Contains(nodeKeyE) {
		t.Errorf("successors of %s should contain %s", "B", "E")
	}

	// check the information of node C
	predecessorsC := testDeletableMemory.predecessors[nodeKeyC]
	successorsC := testDeletableMemory.successors[nodeKeyC]
	if !predecessorsC.Contains(nodeKeyA) {
		t.Errorf("predecessors of %s should contain %s", "C", "A")
	}
	if !successorsC.Contains(nodeKeyE) {
		t.Errorf("successors of %s should contain %s", "C", "E")
	}
	if !successorsC.Contains(nodeKeyF) {
		t.Errorf("successors of %s should contain %s", "C", "F")
	}

	// check the information of node D
	predecessorsD := testDeletableMemory.predecessors[nodeKeyD]
	successorsD := testDeletableMemory.successors[nodeKeyD]
	if !predecessorsD.Contains(nodeKeyA) {
		t.Errorf("predecessors of %s should contain %s", "D", "A")
	}
	if !successorsD.Contains(nodeKeyF) {
		t.Errorf("successors of %s should contain %s", "D", "F")
	}

	// check the information of node E
	predecessorsE := testDeletableMemory.predecessors[nodeKeyE]
	successorsE := testDeletableMemory.successors[nodeKeyE]
	if !predecessorsE.Contains(nodeKeyB) {
		t.Errorf("predecessors of %s should contain %s", "E", "B")
	}
	if !predecessorsE.Contains(nodeKeyC) {
		t.Errorf("predecessors of %s should contain %s", "E", "C")
	}
	for range successorsE {
		t.Errorf("successors of %s should be empty", "E")
	}

	// check the information of node F
	predecessorsF := testDeletableMemory.predecessors[nodeKeyF]
	successorsF := testDeletableMemory.successors[nodeKeyF]
	if !predecessorsF.Contains(nodeKeyC) {
		t.Errorf("predecessors of %s should contain %s", "F", "C")
	}
	if !predecessorsF.Contains(nodeKeyD) {
		t.Errorf("predecessors of %s should contain %s", "F", "D")
	}
	for range successorsF {
		t.Errorf("successors of %s should be empty", "E")
	}

	// check the Predecessors of node C is node A
	predsC, err := testDeletableMemory.Predecessors(ctx, descC)
	if err != nil {
		t.Errorf("testFetcher.Predecessors() error = %v", err)
	}
	expectedLength := 1
	if len(predsC) != expectedLength {
		t.Errorf("%s should have length %d", "predsC", expectedLength)
	}
	if !reflect.DeepEqual(predsC[0], descA) {
		t.Errorf("incorrect predecessor result")
	}

	// check the Predecessors of node F are node C and node D
	predsF, err := testDeletableMemory.Predecessors(ctx, descF)
	if err != nil {
		t.Errorf("testFetcher.Predecessors() error = %v", err)
	}
	expectedLength = 2
	if len(predsF) != expectedLength {
		t.Errorf("%s should have length %d", "predsF", expectedLength)
	}
	for _, pred := range predsF {
		if !reflect.DeepEqual(pred, descC) && !reflect.DeepEqual(pred, descD) {
			t.Errorf("incorrect predecessor result")
		}
	}

	// remove node C and check the stored information
	testDeletableMemory.Remove(ctx, descC)
	if predecessorsE.Contains(nodeKeyC) {
		t.Errorf("predecessors of %s should not contain %s", "E", "C")
	}
	if predecessorsF.Contains(nodeKeyC) {
		t.Errorf("predecessors of %s should not contain %s", "F", "C")
	}
	if !successorsA.Contains(nodeKeyC) {
		t.Errorf("successors of %s should still contain %s", "A", "C")
	}
	if _, exists := testDeletableMemory.successors[nodeKeyC]; exists {
		t.Errorf("testDeletableMemory.successors should not contain the entry of %v", "C")
	}

	// remove node A and check the stored information
	testDeletableMemory.Remove(ctx, descA)
	if predecessorsD.Contains(nodeKeyA) {
		t.Errorf("predecessors of %s should not contain %s", "D", "A")
	}
	if predecessorsB.Contains(nodeKeyA) {
		t.Errorf("predecessors of %s should not contain %s", "B", "A")
	}
	if _, exists := testDeletableMemory.successors[nodeKeyA]; exists {
		t.Errorf("testDeletableMemory.successors should not contain the entry of %v", "A")
	}
}
