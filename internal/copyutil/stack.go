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
	"errors"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

type Item struct {
	Value ocispec.Descriptor
	Depth int
}

// type Stack struct {
// 	items []Item
// }

// func (s *Stack) IsEmpty() bool {
// 	return len(s.items) == 0
// }

// func (s *Stack) Push(i Item) {
// 	s.items = append(s.items, i)
// }

// func (s *Stack) Pop() (Item, error) {
// 	if s.IsEmpty() {
// 		return Item{}, errors.New("empty stack")
// 	}

// 	last := len(s.items) - 1
// 	top := s.items[last]
// 	s.items = s.items[:last]
// 	return top, nil

// }

type Stack []Item

func (s Stack) IsEmpty() bool {
	return len(s) == 0
}

func (s Stack) Push(i Item) Stack {
	return append(s, i)
}

func (s Stack) Pop() (Stack, Item, error) {
	if s.IsEmpty() {
		return nil, Item{}, errors.New("empty stack")
	}

	last := len(s) - 1
	top := s[last]
	return s[:last], top, nil
}
