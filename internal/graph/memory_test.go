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

// +------------------------------+
// |                              |
// |  +-----------+               |
// |  |A(manifest)|               |
// |  +-----+-----+               |
// |        |                     |
// |        +------------+        |
// |        |            |        |
// |        v            v        |
// |  +-----+-----+  +---+----+   |
// |  |B(manifest)|  |C(layer)|   |
// |  +-----+-----+  +--------+   |
// |        |                     |
// |        v                     |
// |    +---+----+                |
// |    |D(layer)|                |
// |    +--------+                |
// |                              |
// |------------------------------+
func TestMemory_IndexAndRemove(t *testing.T) {
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

	// prepare the content in the fetcher, so that it can be used to test Index
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
	// 1. verify its existence in testMemory.nodes
	if _, exists := testMemory.nodes[nodeKeyD]; !exists {
		t.Errorf("nodes entry of %s should exist", "D")
	}
	// 2. verify that the entry of D exists in testMemory.successors and it's empty
	successorsD, exists := testMemory.successors[nodeKeyD]
	if !exists {
		t.Errorf("successor entry of %s should exist", "D")
	}
	if successorsD == nil {
		t.Errorf("successors of %s should be an empty set, not nil", "D")
	}
	if len(successorsD) != 0 {
		t.Errorf("successors of %s should be empty", "D")
	}
	// 3. there should be no entry of D in testMemory.predecessors yet
	_, exists = testMemory.predecessors[nodeKeyD]
	if exists {
		t.Errorf("predecessor entry of %s should not exist yet", "D")
	}

	// index and check the information of node C
	testMemory.Index(ctx, testFetcher, descC)
	// 1. verify its existence in memory.nodes
	if _, exists := testMemory.nodes[nodeKeyC]; !exists {
		t.Errorf("nodes entry of %s should exist", "C")
	}
	// 2. verify that the entry of C exists in testMemory.successors and it's empty
	successorsC, exists := testMemory.successors[nodeKeyC]
	if !exists {
		t.Errorf("successor entry of %s should exist", "C")
	}
	if successorsC == nil {
		t.Errorf("successors of %s should be an empty set, not nil", "C")
	}
	if len(successorsC) != 0 {
		t.Errorf("successors of %s should be empty", "C")
	}
	// 3. there should be no entry of C in testMemory.predecessors yet
	_, exists = testMemory.predecessors[nodeKeyC]
	if exists {
		t.Errorf("predecessor entry of %s should not exist yet", "C")
	}

	// index and check the information of node A
	testMemory.Index(ctx, testFetcher, descA)
	// 1. verify its existence in testMemory.nodes
	if _, exists := testMemory.nodes[nodeKeyA]; !exists {
		t.Errorf("nodes entry of %s should exist", "A")
	}
	// 2. verify that the entry of A exists in testMemory.successors and it contains
	// node B and node D
	successorsA, exists := testMemory.successors[nodeKeyA]
	if !exists {
		t.Errorf("successor entry of %s should exist", "A")
	}
	if successorsA == nil {
		t.Errorf("successors of %s should be a set, not nil", "A")
	}
	if !successorsA.Contains(nodeKeyB) {
		t.Errorf("successors of %s should contain %s", "A", "B")
	}
	if !successorsA.Contains(nodeKeyD) {
		t.Errorf("successors of %s should contain %s", "A", "D")
	}
	// 3. verify that node A exists in the predecessors lists of its successors.
	// there should be an entry of D in testMemory.predecessors by now and it
	// should contain A but not B
	predecessorsD, exists := testMemory.predecessors[nodeKeyD]
	if !exists {
		t.Errorf("predecessor entry of %s should exist by now", "D")
	}
	if !predecessorsD.Contains(nodeKeyA) {
		t.Errorf("predecessors of %s should contain %s", "D", "A")
	}
	if predecessorsD.Contains(nodeKeyB) {
		t.Errorf("predecessors of %s should not contain %s yet", "D", "B")
	}
	// there should be an entry of B in testMemory.predecessors now
	// and it should contain A
	predecessorsB, exists := testMemory.predecessors[nodeKeyB]
	if !exists {
		t.Errorf("predecessor entry of %s should exist by now", "B")
	}
	if !predecessorsB.Contains(nodeKeyA) {
		t.Errorf("predecessors of %s should contain %s", "B", "A")
	}
	// 4. there should be no entry of A in testMemory.predecessors
	_, exists = testMemory.predecessors[nodeKeyA]
	if exists {
		t.Errorf("predecessor entry of %s should not exist", "A")
	}

	// index and check the information of node B
	testMemory.Index(ctx, testFetcher, descB)
	// 1. verify its existence in testMemory.nodes
	if _, exists := testMemory.nodes[nodeKeyB]; !exists {
		t.Errorf("nodes entry of %s should exist", "B")
	}
	// 2. verify that the entry of B exists in testMemory.successors and it contains
	// node C and node D
	successorsB, exists := testMemory.successors[nodeKeyB]
	if !exists {
		t.Errorf("successor entry of %s should exist", "B")
	}
	if successorsB == nil {
		t.Errorf("successors of %s should be a set, not nil", "B")
	}
	if !successorsB.Contains(nodeKeyC) {
		t.Errorf("successors of %s should contain %s", "B", "C")
	}
	if !successorsB.Contains(nodeKeyD) {
		t.Errorf("successors of %s should contain %s", "B", "D")
	}
	// 3. verify that node B exists in the predecessors lists of its successors.
	// there should be an entry of C in testMemory.predecessors by now
	// and it should contain B
	predecessorsC, exists := testMemory.predecessors[nodeKeyC]
	if !exists {
		t.Errorf("predecessor entry of %s should exist by now", "C")
	}
	if !predecessorsC.Contains(nodeKeyB) {
		t.Errorf("predecessors of %s should contain %s", "C", "B")
	}
	// predecessors of D should have been updated now to have node A and B
	if !predecessorsD.Contains(nodeKeyB) {
		t.Errorf("predecessors of %s should contain %s", "D", "B")
	}
	if !predecessorsD.Contains(nodeKeyA) {
		t.Errorf("predecessors of %s should contain %s", "D", "A")
	}

	// remove node B and check the stored information
	testMemory.Remove(descB)
	// 1. verify that node B no longer exists in testMemory.nodes
	if _, exists := testMemory.nodes[nodeKeyB]; exists {
		t.Errorf("nodes entry of %s should no longer exist", "B")
	}
	// 2. verify B' predecessors info: B's entry in testMemory.predecessors should
	// still exist, since its predecessor A still exists
	predecessorsB, exists = testMemory.predecessors[nodeKeyB]
	if !exists {
		t.Errorf("testDeletableMemory.predecessors should still contain the entry of %s", "B")
	}
	if !predecessorsB.Contains(nodeKeyA) {
		t.Errorf("predecessors of %s should still contain %s", "B", "A")
	}
	// 3. verify B' successors info: B's entry in testMemory.successors should no
	// longer exist
	if _, exists := testMemory.successors[nodeKeyB]; exists {
		t.Errorf("testDeletableMemory.successors should not contain the entry of %s", "B")
	}
	// 4. verify B' predecessors' successors info: B should still exist in A's
	// successors
	if !successorsA.Contains(nodeKeyB) {
		t.Errorf("successors of %s should still contain %s", "A", "B")
	}
	// 5. verify B' successors' predecessors info: C's entry in testMemory.predecessors
	// should no longer exist, since C's only predecessor B is already deleted
	if _, exists = testMemory.predecessors[nodeKeyC]; exists {
		t.Errorf("predecessor entry of %s should no longer exist by now, since all its predecessors have been deleted", "C")
	}
	// B should no longer exist in D's predecessors
	if predecessorsD.Contains(nodeKeyB) {
		t.Errorf("predecessors of %s should not contain %s", "D", "B")
	}
	// but A still exists in D's predecessors
	if !predecessorsD.Contains(nodeKeyA) {
		t.Errorf("predecessors of %s should still contain %s", "D", "A")
	}

	// remove node A and check the stored information
	testMemory.Remove(descA)
	// 1. verify that node A no longer exists in testMemory.nodes
	if _, exists := testMemory.nodes[nodeKeyA]; exists {
		t.Errorf("nodes entry of %s should no longer exist", "A")
	}
	// 2. verify A' successors info: A's entry in testMemory.successors should no
	// longer exist
	if _, exists := testMemory.successors[nodeKeyA]; exists {
		t.Errorf("testDeletableMemory.successors should not contain the entry of %s", "A")
	}
	// 3. verify A' successors' predecessors info: D's entry in testMemory.predecessors
	// should no longer exist, since all predecessors of D are already deleted
	if _, exists = testMemory.predecessors[nodeKeyD]; exists {
		t.Errorf("predecessor entry of %s should no longer exist by now, since all its predecessors have been deleted", "D")
	}
	// B's entry in testMemory.predecessors should no longer exist, since B's only
	// predecessor A is already deleted
	if _, exists = testMemory.predecessors[nodeKeyB]; exists {
		t.Errorf("predecessor entry of %s should no longer exist by now, since all its predecessors have been deleted", "B")
	}
}

// +-----------------------------------------------+
// |                                               |
// |                   +--------+                  |
// |                   |A(index)|                  |
// |                   +---+----+                  |
// |                       |                       |
// |       -+--------------+--------------+-       |
// |        |              |              |        |
// |  +-----v-----+  +-----v-----+  +-----v-----+  |
// |  |B(manifest)|  |C(manifest)|  |D(manifest)|  |
// |  +--------+--+  ++---------++  +--+--------+  |
// |           |      |         |      |           |
// |           |      |         |      |           |
// |           v      v         v      v           |
// |          ++------++       ++------++          |
// |          |E(layer)|       |F(layer)|          |
// |          +--------+       +--------+          |
// |                                               |
// +-----------------------------------------------+
func TestMemory_IndexAllAndPredecessors(t *testing.T) {
	testFetcher := cas.NewMemory()
	testMemory := NewMemory()
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
			Config: ocispec.Descriptor{MediaType: "test config"},
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

	// prepare the content in the fetcher, so that it can be used to test IndexAll
	testContents := []ocispec.Descriptor{descE, descF, descB, descC, descD, descA}
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
	if !bytes.Equal(got, blobs[5]) {
		t.Errorf("testFetcher.Fetch() = %v, want %v", got, blobs[4])
	}

	nodeKeyA := descriptor.FromOCI(descA)
	nodeKeyB := descriptor.FromOCI(descB)
	nodeKeyC := descriptor.FromOCI(descC)
	nodeKeyD := descriptor.FromOCI(descD)
	nodeKeyE := descriptor.FromOCI(descE)
	nodeKeyF := descriptor.FromOCI(descF)

	// index node A into testMemory using IndexAll
	testMemory.IndexAll(ctx, testFetcher, descA)

	// check the information of node A
	// 1. verify that node A exists in testMemory.nodes
	if _, exists := testMemory.nodes[nodeKeyA]; !exists {
		t.Errorf("nodes entry of %s should exist", "A")
	}
	// 2. verify that there is no entry of A in predecessors
	if _, exists := testMemory.predecessors[nodeKeyA]; exists {
		t.Errorf("there should be no entry of %s in predecessors", "A")
	}
	// 3. verify that A has successors B, C, D
	successorsA, exists := testMemory.successors[nodeKeyA]
	if !exists {
		t.Errorf("there should be an entry of %s in successors", "A")
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
	// 1. verify that node B exists in testMemory.nodes
	if _, exists := testMemory.nodes[nodeKeyB]; !exists {
		t.Errorf("nodes entry of %s should exist", "B")
	}
	// 2. verify that B has node A in its predecessors
	predecessorsB := testMemory.predecessors[nodeKeyB]
	if !predecessorsB.Contains(nodeKeyA) {
		t.Errorf("predecessors of %s should contain %s", "B", "A")
	}
	// 3. verify that B has node E in its successors
	successorsB := testMemory.successors[nodeKeyB]
	if !successorsB.Contains(nodeKeyE) {
		t.Errorf("successors of %s should contain %s", "B", "E")
	}

	// check the information of node C
	// 1. verify that node C exists in testMemory.nodes
	if _, exists := testMemory.nodes[nodeKeyC]; !exists {
		t.Errorf("nodes entry of %s should exist", "C")
	}
	// 2. verify that C has node A in its predecessors
	predecessorsC := testMemory.predecessors[nodeKeyC]
	if !predecessorsC.Contains(nodeKeyA) {
		t.Errorf("predecessors of %s should contain %s", "C", "A")
	}
	// 3. verify that C has node E and F in its successors
	successorsC := testMemory.successors[nodeKeyC]
	if !successorsC.Contains(nodeKeyE) {
		t.Errorf("successors of %s should contain %s", "C", "E")
	}
	if !successorsC.Contains(nodeKeyF) {
		t.Errorf("successors of %s should contain %s", "C", "F")
	}

	// check the information of node D
	// 1. verify that node D exists in testMemory.nodes
	if _, exists := testMemory.nodes[nodeKeyD]; !exists {
		t.Errorf("nodes entry of %s should exist", "D")
	}
	// 2. verify that D has node A in its predecessors
	predecessorsD := testMemory.predecessors[nodeKeyD]
	if !predecessorsD.Contains(nodeKeyA) {
		t.Errorf("predecessors of %s should contain %s", "D", "A")
	}
	// 3. verify that D has node F in its successors
	successorsD := testMemory.successors[nodeKeyD]
	if !successorsD.Contains(nodeKeyF) {
		t.Errorf("successors of %s should contain %s", "D", "F")
	}

	// check the information of node E
	// 1. verify that node E exists in testMemory.nodes
	if _, exists := testMemory.nodes[nodeKeyE]; !exists {
		t.Errorf("nodes entry of %s should exist", "E")
	}
	// 2. verify that E has node B and C in its predecessors
	predecessorsE := testMemory.predecessors[nodeKeyE]
	if !predecessorsE.Contains(nodeKeyB) {
		t.Errorf("predecessors of %s should contain %s", "E", "B")
	}
	if !predecessorsE.Contains(nodeKeyC) {
		t.Errorf("predecessors of %s should contain %s", "E", "C")
	}
	// 3. verify that E has an entry in successors and it's empty
	successorsE, exists := testMemory.successors[nodeKeyE]
	if !exists {
		t.Errorf("entry %s should exist in testMemory.successors", "E")
	}
	if successorsE == nil {
		t.Errorf("successors of %s should be an empty set, not nil", "E")
	}
	if len(successorsE) != 0 {
		t.Errorf("successors of %s should be empty", "E")
	}

	// check the information of node F
	// 1. verify that node F exists in testMemory.nodes
	if _, exists := testMemory.nodes[nodeKeyF]; !exists {
		t.Errorf("nodes entry of %s should exist", "F")
	}
	// 2. verify that F has node C and D in its predecessors
	predecessorsF := testMemory.predecessors[nodeKeyF]
	if !predecessorsF.Contains(nodeKeyC) {
		t.Errorf("predecessors of %s should contain %s", "F", "C")
	}
	if !predecessorsF.Contains(nodeKeyD) {
		t.Errorf("predecessors of %s should contain %s", "F", "D")
	}
	// 3. verify that F has an entry in successors and it's empty
	successorsF, exists := testMemory.successors[nodeKeyF]
	if !exists {
		t.Errorf("entry %s should exist in testMemory.successors", "F")
	}
	if successorsF == nil {
		t.Errorf("successors of %s should be an empty set, not nil", "F")
	}
	if len(successorsF) != 0 {
		t.Errorf("successors of %s should be empty", "F")
	}

	// check that the Predecessors of node C is node A
	predsC, err := testMemory.Predecessors(ctx, descC)
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

	// check that the Predecessors of node F are node C and node D
	predsF, err := testMemory.Predecessors(ctx, descF)
	if err != nil {
		t.Errorf("testFetcher.Predecessors() error = %v", err)
	}
	expectedLength = 2
	if len(predsF) != expectedLength {
		t.Errorf("%s should have length %d", "predsF", expectedLength)
	}
	for _, pred := range predsF {
		if !reflect.DeepEqual(pred, descC) && !reflect.DeepEqual(pred, descD) {
			t.Errorf("incorrect predecessor results")
		}
	}

	// remove node C and check the stored information
	testMemory.Remove(descC)
	if predecessorsE.Contains(nodeKeyC) {
		t.Errorf("predecessors of %s should not contain %s", "E", "C")
	}
	if predecessorsF.Contains(nodeKeyC) {
		t.Errorf("predecessors of %s should not contain %s", "F", "C")
	}
	if !successorsA.Contains(nodeKeyC) {
		t.Errorf("successors of %s should still contain %s", "A", "C")
	}
	if _, exists := testMemory.successors[nodeKeyC]; exists {
		t.Errorf("testMemory.successors should not contain the entry of %s", "C")
	}
	if _, exists := testMemory.predecessors[nodeKeyC]; !exists {
		t.Errorf("entry %s in predecessors should still exists since it still has at least one predecessor node present", "C")
	}

	// remove node A and check the stored information
	testMemory.Remove(descA)
	if _, exists := testMemory.predecessors[nodeKeyB]; exists {
		t.Errorf("entry %s in predecessors should no longer exists", "B")
	}
	if _, exists := testMemory.predecessors[nodeKeyC]; exists {
		t.Errorf("entry %s in predecessors should no longer exists", "C")
	}
	if _, exists := testMemory.predecessors[nodeKeyD]; exists {
		t.Errorf("entry %s in predecessors should no longer exists", "D")
	}
	if _, exists := testMemory.successors[nodeKeyA]; exists {
		t.Errorf("testDeletableMemory.successors should not contain the entry of %s", "A")
	}

	// check that the Predecessors of node D is empty
	predsD, err := testMemory.Predecessors(ctx, descD)
	if err != nil {
		t.Errorf("testFetcher.Predecessors() error = %v", err)
	}
	if predsD != nil {
		t.Errorf("%s should be nil", "predsD")
	}

	// check that the Predecessors of node E is node B
	predsE, err := testMemory.Predecessors(ctx, descE)
	if err != nil {
		t.Errorf("testFetcher.Predecessors() error = %v", err)
	}
	expectedLength = 1
	if len(predsE) != expectedLength {
		t.Errorf("%s should have length %d", "predsE", expectedLength)
	}
	if !reflect.DeepEqual(predsE[0], descB) {
		t.Errorf("incorrect predecessor result")
	}
}

func TestMemory_DigestSet(t *testing.T) {
	testFetcher := cas.NewMemory()
	testMemory := NewMemory()
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
			Config: ocispec.Descriptor{MediaType: "test config"},
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

	// prepare the content in the fetcher, so that it can be used to test IndexAll
	testContents := []ocispec.Descriptor{descE, descF, descB, descC, descD, descA}
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
	if !bytes.Equal(got, blobs[5]) {
		t.Errorf("testFetcher.Fetch() = %v, want %v", got, blobs[4])
	}

	// index node A into testMemory using IndexAll
	testMemory.IndexAll(ctx, testFetcher, descA)
	digestSet := testMemory.DigestSet()
	for i := 0; i < len(blobs); i++ {
		if exists := digestSet.Contains(descriptors[i].Digest); exists != true {
			t.Errorf("digest of blob[%d] should exist in digestSet", i)
		}
	}
}

func TestMemory_Exists(t *testing.T) {
	testFetcher := cas.NewMemory()
	testMemory := NewMemory()
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
			Config: ocispec.Descriptor{MediaType: "test config"},
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

	// prepare the content in the fetcher, so that it can be used to test IndexAll
	testContents := []ocispec.Descriptor{descE, descF, descB, descC, descD, descA}
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
	if !bytes.Equal(got, blobs[5]) {
		t.Errorf("testFetcher.Fetch() = %v, want %v", got, blobs[4])
	}

	// index node A into testMemory using IndexAll
	testMemory.IndexAll(ctx, testFetcher, descA)
	for i := 0; i < len(blobs); i++ {
		if exists := testMemory.Exists(descriptors[i]); exists != true {
			t.Errorf("digest of blob[%d] should exist in digestSet", i)
		}
	}
}
