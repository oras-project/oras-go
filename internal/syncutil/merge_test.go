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

package syncutil

import (
	"context"
	"testing"

	"golang.org/x/sync/errgroup"
)

func TestMerge(t *testing.T) {
	var merge Merge[int]

	// generate expected result
	size := 100
	factor := -1
	var expected int
	for i := 1; i < size; i++ {
		expected += i * factor
	}

	// test merge
	ctx := context.Background()
	eg, _ := errgroup.WithContext(ctx)
	var result int
	for i := 1; i < size; i++ {
		eg.Go(func(num int) func() error {
			return func() error {
				var f int
				getFactor := func() error {
					f = factor
					return nil
				}
				calculate := func(items []int) error {
					for _, item := range items {
						result += item * f
					}
					return nil
				}
				return merge.Do(num, getFactor, calculate)
			}
		}(i))
	}
	if err := eg.Wait(); err != nil {
		t.Errorf("Merge.Do() error = %v, wantErr %v", err, nil)
	}
	if result != expected {
		t.Errorf("Merge.Do() = %v, want %v", result, expected)
	}
}
