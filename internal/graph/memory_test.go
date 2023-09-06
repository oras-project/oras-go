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
	"context"
	"sync"
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

// 条件：
// 1. 只要node被index（作为函数的第二个输入），node就在predecessors，successors，indexed中有entry
// 2. 只要node被Removed from index，node就在predecessors，successors，indexed中都没有entry
// 3. 只有node被index（作为函数的第二个输入），successors中才有其entry
// 4. successors中的内容与node中的内容一致，只要entry还在，其内容就不会改变

// GC情况：
// predecessors中可能有空的map，如下图中B被删除后，C还在predecessors中有entry但内容为空
/*
A--->B--->C
|    |
|    +--->D
|         ^
|         |
+---------+
*/
func TestMemory_index(t *testing.T) {
	ctx := context.Background()
	testMemory := NewMemory()

	// test 1: index "A -> B"
	testMemory.index(ctx, A, []ocispec.Descriptor{B})

	// check the memory: A exists in testMemory.predecessors, successors and indexed,
	// B ONLY exists in predecessors
	_, exists := testMemory.predecessors.Load(AKey)
	if !exists {
		t.Errorf("could not find the entry of %v in predecessors", A)
	}
	_, exists = testMemory.successors.Load(AKey)
	if !exists {
		t.Errorf("could not find the entry of %v in successors", A)
	}
	_, exists = testMemory.indexed.Load(AKey)
	if !exists {
		t.Errorf("could not find the entry of %v in indexed", A)
	}
	_, exists = testMemory.predecessors.Load(BKey)
	if !exists {
		t.Errorf("could not find the entry of %v in predecessors", B)
	}
	_, exists = testMemory.successors.Load(BKey)
	if exists {
		t.Errorf("%v should not exist in successors", B)
	}
	_, exists = testMemory.indexed.Load(BKey)
	if exists {
		t.Errorf("%v should not exist in indexed", B)
	}

	// predecessors[B] contains A, successors[A] contains B
	value, _ := testMemory.predecessors.Load(BKey)
	BPreds := value.(*sync.Map)
	_, exists = BPreds.Load(AKey)
	if !exists {
		t.Errorf("could not find %v in predecessors of %v", A, B)
	}
	value, _ = testMemory.successors.Load(AKey)
	ASuccs := value.(*sync.Map)
	_, exists = ASuccs.Load(BKey)
	if !exists {
		t.Errorf("could not find %v in successors of %v", B, A)
	}

	// test 2: index "B -> C, B -> D"
	testMemory.index(ctx, B, []ocispec.Descriptor{C, D})

	// check the memory: B exists in testMemory.predecessors, successors and indexed,
	// C, D ONLY exists in predecessors
	_, exists = testMemory.predecessors.Load(BKey)
	if !exists {
		t.Errorf("could not find the entry of %v in predecessors", B)
	}
	_, exists = testMemory.successors.Load(BKey)
	if !exists {
		t.Errorf("could not find the entry of %v in successors", B)
	}
	_, exists = testMemory.indexed.Load(BKey)
	if !exists {
		t.Errorf("could not find the entry of %v in indexed", B)
	}
	_, exists = testMemory.predecessors.Load(CKey)
	if !exists {
		t.Errorf("could not find the entry of %v in predecessors", C)
	}
	_, exists = testMemory.successors.Load(CKey)
	if exists {
		t.Errorf("%v should not exist in successors", C)
	}
	_, exists = testMemory.indexed.Load(CKey)
	if exists {
		t.Errorf("%v should not exist in indexed", C)
	}
	_, exists = testMemory.predecessors.Load(DKey)
	if !exists {
		t.Errorf("could not find the entry of %v in predecessors", D)
	}
	_, exists = testMemory.successors.Load(DKey)
	if exists {
		t.Errorf("%v should not exist in successors", D)
	}
	_, exists = testMemory.indexed.Load(DKey)
	if exists {
		t.Errorf("%v should not exist in indexed", D)
	}
	// predecessors[C] contains B, predecessors[D] contains B,
	// successors[B] contains C and D
	value, _ = testMemory.predecessors.Load(CKey)
	CPreds := value.(*sync.Map)
	_, exists = CPreds.Load(BKey)
	if !exists {
		t.Errorf("could not find %v in predecessors of %v", B, C)
	}
	value, _ = testMemory.predecessors.Load(DKey)
	DPreds := value.(*sync.Map)
	_, exists = DPreds.Load(BKey)
	if !exists {
		t.Errorf("could not find %v in predecessors of %v", B, D)
	}
	value, _ = testMemory.successors.Load(BKey)
	BSuccs := value.(*sync.Map)
	_, exists = BSuccs.Load(CKey)
	if !exists {
		t.Errorf("could not find %v in successors of %v", C, B)
	}
	_, exists = BSuccs.Load(DKey)
	if !exists {
		t.Errorf("could not find %v in successors of %v", D, B)
	}

	// test 3: index "A -> D"
	testMemory.index(ctx, A, []ocispec.Descriptor{D})

	// predecessors[D] contains A and B, successors[A] contains B and D
	value, _ = testMemory.predecessors.Load(DKey)
	DPreds = value.(*sync.Map)
	_, exists = DPreds.Load(AKey)
	if !exists {
		t.Errorf("could not find %v in predecessors of %v", A, D)
	}
	_, exists = DPreds.Load(BKey)
	if !exists {
		t.Errorf("could not find %v in predecessors of %v", B, D)
	}
	value, _ = testMemory.successors.Load(AKey)
	ASuccs = value.(*sync.Map)
	_, exists = ASuccs.Load(BKey)
	if !exists {
		t.Errorf("could not find %v in successors of %v", B, A)
	}
	_, exists = ASuccs.Load(DKey)
	if !exists {
		t.Errorf("could not find %v in successors of %v", D, A)
	}

	// check the memory: C and D have not been indexed, so C, D should not
	// exist in indexed and successors
	_, exists = testMemory.successors.Load(CKey)
	if exists {
		t.Errorf("%v should not exist in successors", C)
	}
	_, exists = testMemory.indexed.Load(CKey)
	if exists {
		t.Errorf("%v should not exist in indexed", C)
	}
	_, exists = testMemory.successors.Load(DKey)
	if exists {
		t.Errorf("%v should not exist in successors", D)
	}
	_, exists = testMemory.indexed.Load(DKey)
	if exists {
		t.Errorf("%v should not exist in indexed", D)
	}

	// test 4: delete B
	err := testMemory.RemoveFromIndex(ctx, B)
	if err != nil {
		t.Errorf("got error when removing %v from index: %v", B, err)
	}

	// check the memory: B should NOT exist in predecessors, successors and indexed
	_, exists = testMemory.predecessors.Load(BKey)
	if exists {
		t.Errorf("%v should not exist in predecessors", B)
	}
	_, exists = testMemory.successors.Load(BKey)
	if exists {
		t.Errorf("%v should not exist in successors", B)
	}
	_, exists = testMemory.indexed.Load(BKey)
	if exists {
		t.Errorf("%v should not exist in indexed", B)
	}

	// B should STILL exist in successors[A]
	value, _ = testMemory.successors.Load(AKey)
	ASuccs = value.(*sync.Map)
	_, exists = ASuccs.Load(BKey)
	if !exists {
		t.Errorf("could not find %v in successors of %v", B, A)
	}

	// B should NOT exist in predecessors[C], predecessors[D]
	value, _ = testMemory.predecessors.Load(CKey)
	CPreds = value.(*sync.Map)
	_, exists = CPreds.Load(BKey)
	if exists {
		t.Errorf("should not find %v in predecessors of %v", B, C)
	}
	value, _ = testMemory.predecessors.Load(DKey)
	DPreds = value.(*sync.Map)
	_, exists = DPreds.Load(BKey)
	if exists {
		t.Errorf("should not find %v in predecessors of %v", B, D)
	}

	// A should STILL exist in predecessors[D]
	_, exists = DPreds.Load(AKey)
	if !exists {
		t.Errorf("could not find %v in predecessors of %v", A, D)
	}
}
