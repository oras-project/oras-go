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

// func validateMemory(m *Memory, node ocispec.Descriptor, successors []ocispec.Descriptor) error {

// 	return nil
// }

// func TestMemory_index(t *testing.T) {
// 	tests := []struct {
// 		name       string
// 		node       ocispec.Descriptor
// 		successors []ocispec.Descriptor
// 	}{
// 		{"A->B",
// 			ocispec.Descriptor{MediaType: "A"},
// 			[]ocispec.Descriptor{
// 				{MediaType: "B"},
// 			}},
// 	}
// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			m := NewMemory()
// 			m.index(context.Background(), tt.node, tt.successors)
// 		})
// 	}
// }
