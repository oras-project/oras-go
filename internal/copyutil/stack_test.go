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

package copyutil

import (
	"reflect"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestStack(t *testing.T) {
	stack := &Stack{}
	isEmpty := stack.IsEmpty()
	if !isEmpty {
		t.Errorf("Stack.IsEmpty() = %v, want %v", isEmpty, true)
	}

	items := []Item{
		{ocispec.Descriptor{}, 0},
		{ocispec.Descriptor{}, 1},
		{ocispec.Descriptor{}, 2},
	}
	for _, item := range items {
		stack.Push(item)
	}

	i := len(items) - 1
	for !stack.IsEmpty() {
		got, err := stack.Pop()
		if err != nil {
			t.Fatalf("Stack.Pop() error = %v, wantErr %v", err, false)
		}
		if !reflect.DeepEqual(got, items[i]) {
			t.Errorf("Stack.Pop() = %v, want %v", got, items[i])
		}
		i--
	}

	_, err := stack.Pop()
	if err == nil {
		t.Errorf("Stack.Pop() error = %v, wantErr %v", err, ErrEmptyStack)
	}
}
