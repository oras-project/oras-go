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

// 当前条件（后续可能改动）：
// 1. 只要node被index（作为函数的第二个输入），node就在predecessors，successors中都有entry
// 2. 只要node被Removed from index，node就在predecessors，successors中都没有entry
// 3. 只有node被index（作为函数的第二个输入），successors中才有其entry
// 4. successors中的内容与node中的内容一致，只要entry还在，其内容就不会改变
// 5. 变量indexed的作用和行为和原始版本一样，没有加以变动
// 6. 删除一个node的时候，indexed里面的entry也会删除。

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
func TestMemoryWithDelete_indexAndDelete(t *testing.T) {
	ctx := context.Background()
	testMemoryWithDelete := NewMemoryWithDelete()

	// test 1: index "A -> B"
	testMemoryWithDelete.index(ctx, A, []ocispec.Descriptor{B})

	// check the memory: A exists in predecessors and successors
	// B ONLY exists in predecessors
	_, exists := testMemoryWithDelete.predecessors.Load(AKey)
	if !exists {
		t.Errorf("could not find the entry of %v in predecessors", A)
	}
	_, exists = testMemoryWithDelete.successors[AKey]
	if !exists {
		t.Errorf("could not find the entry of %v in successors", A)
	}
	_, exists = testMemoryWithDelete.predecessors.Load(BKey)
	if !exists {
		t.Errorf("could not find the entry of %v in predecessors", B)
	}
	_, exists = testMemoryWithDelete.successors[BKey]
	if exists {
		t.Errorf("%v should not exist in successors", B)
	}

	// predecessors[B] contains A, successors[A] contains B
	value, _ := testMemoryWithDelete.predecessors.Load(BKey)
	BPreds := value.(*sync.Map)
	_, exists = BPreds.Load(AKey)
	if !exists {
		t.Errorf("could not find %v in predecessors of %v", A, B)
	}
	_, exists = testMemoryWithDelete.successors[AKey][BKey]
	if !exists {
		t.Errorf("could not find %v in successors of %v", B, A)
	}

	// test 2: index "B -> C, B -> D"
	testMemoryWithDelete.index(ctx, B, []ocispec.Descriptor{C, D})

	// check the memory: B exists in predecessors and successors,
	// C, D ONLY exists in predecessors
	_, exists = testMemoryWithDelete.predecessors.Load(BKey)
	if !exists {
		t.Errorf("could not find the entry of %v in predecessors", B)
	}
	_, exists = testMemoryWithDelete.successors[BKey]
	if !exists {
		t.Errorf("could not find the entry of %v in successors", B)
	}
	_, exists = testMemoryWithDelete.predecessors.Load(CKey)
	if !exists {
		t.Errorf("could not find the entry of %v in predecessors", C)
	}
	_, exists = testMemoryWithDelete.successors[CKey]
	if exists {
		t.Errorf("%v should not exist in successors", C)
	}
	_, exists = testMemoryWithDelete.predecessors.Load(DKey)
	if !exists {
		t.Errorf("could not find the entry of %v in predecessors", D)
	}
	_, exists = testMemoryWithDelete.successors[DKey]
	if exists {
		t.Errorf("%v should not exist in successors", D)
	}
	// predecessors[C] contains B, predecessors[D] contains B,
	// successors[B] contains C and D
	value, _ = testMemoryWithDelete.predecessors.Load(CKey)
	CPreds := value.(*sync.Map)
	_, exists = CPreds.Load(BKey)
	if !exists {
		t.Errorf("could not find %v in predecessors of %v", B, C)
	}
	value, _ = testMemoryWithDelete.predecessors.Load(DKey)
	DPreds := value.(*sync.Map)
	_, exists = DPreds.Load(BKey)
	if !exists {
		t.Errorf("could not find %v in predecessors of %v", B, D)
	}
	_, exists = testMemoryWithDelete.successors[BKey][CKey]
	if !exists {
		t.Errorf("could not find %v in successors of %v", C, B)
	}
	_, exists = testMemoryWithDelete.successors[BKey][DKey]
	if !exists {
		t.Errorf("could not find %v in successors of %v", D, B)
	}

	// test 3: index "A -> D"
	testMemoryWithDelete.index(ctx, A, []ocispec.Descriptor{D})

	// predecessors[D] contains A and B, successors[A] contains B and D
	value, _ = testMemoryWithDelete.predecessors.Load(DKey)
	DPreds = value.(*sync.Map)
	_, exists = DPreds.Load(AKey)
	if !exists {
		t.Errorf("could not find %v in predecessors of %v", A, D)
	}
	_, exists = DPreds.Load(BKey)
	if !exists {
		t.Errorf("could not find %v in predecessors of %v", B, D)
	}
	_, exists = testMemoryWithDelete.successors[AKey][BKey]
	if !exists {
		t.Errorf("could not find %v in successors of %v", B, A)
	}
	_, exists = testMemoryWithDelete.successors[AKey][DKey]
	if !exists {
		t.Errorf("could not find %v in successors of %v", D, A)
	}

	// check the memory: C and D have not been indexed, so C, D should not
	// exist in successors
	_, exists = testMemoryWithDelete.successors[CKey]
	if exists {
		t.Errorf("%v should not exist in successors", C)
	}
	_, exists = testMemoryWithDelete.successors[DKey]
	if exists {
		t.Errorf("%v should not exist in successors", D)
	}

	// test 4: delete B
	err := testMemoryWithDelete.Remove(ctx, B)
	if err != nil {
		t.Errorf("got error when removing %v from index: %v", B, err)
	}

	// check the memory: B should NOT exist in successors, should still exist
	// in predecessors
	_, exists = testMemoryWithDelete.predecessors.Load(BKey)
	if !exists {
		t.Errorf("%v should exist in predecessors", B)
	}
	_, exists = testMemoryWithDelete.successors[BKey]
	if exists {
		t.Errorf("%v should not exist in successors", B)
	}

	// B should STILL exist in successors[A]
	_, exists = testMemoryWithDelete.successors[AKey][BKey]
	if !exists {
		t.Errorf("could not find %v in successors of %v", B, A)
	}

	// B should NOT exist in predecessors[C], predecessors[D]
	value, _ = testMemoryWithDelete.predecessors.Load(CKey)
	CPreds = value.(*sync.Map)
	_, exists = CPreds.Load(BKey)
	if exists {
		t.Errorf("should not find %v in predecessors of %v", B, C)
	}
	value, _ = testMemoryWithDelete.predecessors.Load(DKey)
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
