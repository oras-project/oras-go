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
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/internal/descriptor"
)

var (
	A    = ocispec.Descriptor{MediaType: "A"}
	AKey = descriptor.FromOCI(A)
	B    = ocispec.Descriptor{MediaType: "B"}
	BKey = descriptor.FromOCI(B)
	C    = ocispec.Descriptor{MediaType: "C"}
	CKey = descriptor.FromOCI(C)
	D    = ocispec.Descriptor{MediaType: "D"}
	DKey = descriptor.FromOCI(D)
)

/*

test 1: index "A -> B"
test 2: index "B -> C, B -> D"
test 3: index "A -> D"
test 4: delete B
test 5: delete A

A--->B--->C
|    |
|    +--->D
|         ^
|         |
+---------+
*/

func TestMemoryWithDelete_indexAndDelete(t *testing.T) {
	// ctx := context.Background()
	// testDeletableMemory := NewDeletableMemory()

	// // test 1: index "A -> B"
	// testMemoryWithDelete.index(ctx, A, []ocispec.Descriptor{B})

	// // check the memory: A exists in predecessors and successors
	// // B ONLY exists in predecessors
	// _, exists := testMemoryWithDelete.predecessors[AKey]
	// if !exists {
	// 	t.Errorf("could not find the entry of %v in predecessors", A)
	// }
	// _, exists = testMemoryWithDelete.successors[AKey]
	// if !exists {
	// 	t.Errorf("could not find the entry of %v in successors", A)
	// }
	// _, exists = testMemoryWithDelete.predecessors[BKey]
	// if !exists {
	// 	t.Errorf("could not find the entry of %v in predecessors", B)
	// }
	// _, exists = testMemoryWithDelete.successors[BKey]
	// if exists {
	// 	t.Errorf("%v should not exist in successors", B)
	// }

	// // predecessors[B] contains A, successors[A] contains B
	// _, exists = testMemoryWithDelete.predecessors[BKey][AKey]
	// if !exists {
	// 	t.Errorf("could not find %v in predecessors of %v", A, B)
	// }
	// _, exists = testMemoryWithDelete.successors[AKey][BKey]
	// if !exists {
	// 	t.Errorf("could not find %v in successors of %v", B, A)
	// }

	// // test 2: index "B -> C, B -> D"
	// testMemoryWithDelete.index(ctx, B, []ocispec.Descriptor{C, D})

	// // check the memory: B exists in predecessors and successors,
	// // C, D ONLY exists in predecessors
	// _, exists = testMemoryWithDelete.predecessors[BKey]
	// if !exists {
	// 	t.Errorf("could not find the entry of %v in predecessors", B)
	// }
	// _, exists = testMemoryWithDelete.successors[BKey]
	// if !exists {
	// 	t.Errorf("could not find the entry of %v in successors", B)
	// }
	// _, exists = testMemoryWithDelete.predecessors[CKey]
	// if !exists {
	// 	t.Errorf("could not find the entry of %v in predecessors", C)
	// }
	// _, exists = testMemoryWithDelete.successors[CKey]
	// if exists {
	// 	t.Errorf("%v should not exist in successors", C)
	// }
	// _, exists = testMemoryWithDelete.predecessors[DKey]
	// if !exists {
	// 	t.Errorf("could not find the entry of %v in predecessors", D)
	// }
	// _, exists = testMemoryWithDelete.successors[DKey]
	// if exists {
	// 	t.Errorf("%v should not exist in successors", D)
	// }
	// // predecessors[C] contains B, predecessors[D] contains B,
	// // successors[B] contains C and D
	// _, exists = testMemoryWithDelete.predecessors[CKey][BKey]
	// if !exists {
	// 	t.Errorf("could not find %v in predecessors of %v", B, C)
	// }
	// _, exists = testMemoryWithDelete.predecessors[DKey][BKey]
	// if !exists {
	// 	t.Errorf("could not find %v in predecessors of %v", B, D)
	// }
	// _, exists = testMemoryWithDelete.successors[BKey][CKey]
	// if !exists {
	// 	t.Errorf("could not find %v in successors of %v", C, B)
	// }
	// _, exists = testMemoryWithDelete.successors[BKey][DKey]
	// if !exists {
	// 	t.Errorf("could not find %v in successors of %v", D, B)
	// }

	// // test 3: index "A -> D"
	// testMemoryWithDelete.index(ctx, A, []ocispec.Descriptor{D})

	// // predecessors[D] contains A and B, successors[A] contains B and D
	// _, exists = testMemoryWithDelete.predecessors[DKey][AKey]
	// if !exists {
	// 	t.Errorf("could not find %v in predecessors of %v", A, D)
	// }
	// _, exists = testMemoryWithDelete.predecessors[DKey][BKey]
	// if !exists {
	// 	t.Errorf("could not find %v in predecessors of %v", B, D)
	// }
	// _, exists = testMemoryWithDelete.successors[AKey][BKey]
	// if !exists {
	// 	t.Errorf("could not find %v in successors of %v", B, A)
	// }
	// _, exists = testMemoryWithDelete.successors[AKey][DKey]
	// if !exists {
	// 	t.Errorf("could not find %v in successors of %v", D, A)
	// }

	// // check the memory: C and D have not been indexed, so C, D should not
	// // exist in successors
	// _, exists = testMemoryWithDelete.successors[CKey]
	// if exists {
	// 	t.Errorf("%v should not exist in successors", C)
	// }
	// _, exists = testMemoryWithDelete.successors[DKey]
	// if exists {
	// 	t.Errorf("%v should not exist in successors", D)
	// }

	// // test 4: delete B
	// err := testMemoryWithDelete.Remove(ctx, B)
	// if err != nil {
	// 	t.Errorf("got error when removing %v from index: %v", B, err)
	// }

	// // check the memory: B should NOT exist in successors, should still exist
	// // in predecessors
	// _, exists = testMemoryWithDelete.predecessors[BKey]
	// if !exists {
	// 	t.Errorf("%v should exist in predecessors", B)
	// }
	// _, exists = testMemoryWithDelete.successors[BKey]
	// if exists {
	// 	t.Errorf("%v should not exist in successors", B)
	// }

	// // B should STILL exist in successors[A]
	// _, exists = testMemoryWithDelete.successors[AKey][BKey]
	// if !exists {
	// 	t.Errorf("could not find %v in successors of %v", B, A)
	// }

	// // B should NOT exist in predecessors[C], predecessors[D]
	// _, exists = testMemoryWithDelete.predecessors[CKey][BKey]
	// if exists {
	// 	t.Errorf("should not find %v in predecessors of %v", B, C)
	// }
	// _, exists = testMemoryWithDelete.predecessors[DKey][BKey]
	// if exists {
	// 	t.Errorf("should not find %v in predecessors of %v", B, D)
	// }

	// // A should STILL exist in predecessors[D]
	// _, exists = testMemoryWithDelete.predecessors[DKey][AKey]
	// if !exists {
	// 	t.Errorf("could not find %v in predecessors of %v", A, D)
	// }

	// // test 5: delete A
	// err = testMemoryWithDelete.Remove(ctx, A)
	// if err != nil {
	// 	t.Errorf("got error when removing %v from index: %v", A, err)
	// }

	// // check the memory: A should NOT exist in successors, should still exist
	// // in predecessors
	// _, exists = testMemoryWithDelete.predecessors[AKey]
	// if !exists {
	// 	t.Errorf("%v should exist in predecessors", A)
	// }
	// _, exists = testMemoryWithDelete.successors[AKey]
	// if exists {
	// 	t.Errorf("%v should not exist in successors", A)
	// }

	// // A should NOT exist in predecessors[D]
	// _, exists = testMemoryWithDelete.predecessors[DKey][AKey]
	// if exists {
	// 	t.Errorf("should not find %v in predecessors of %v", A, D)
	// }
}
