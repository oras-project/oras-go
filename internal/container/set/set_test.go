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

package set

import "testing"

func TestSet(t *testing.T) {
	set := make(Set[string])

	// test checking a non-existing key
	key1 := "foo"
	if got := set.Contains(key1); got != false {
		t.Errorf("Set.Contains(%s) = %v, want %v", key1, got, false)
	}

	// test adding a new key
	set.Add(key1)
	if got := set.Contains(key1); got != true {
		t.Errorf("Set.Contains(%s) = %v, want %v", key1, got, true)
	}
	if got := len(set); got != 1 {
		t.Errorf("len(Set) = %v, want %v", got, 1)
	}

	// test adding an existing key
	set.Add(key1)
	if got := set.Contains(key1); got != true {
		t.Errorf("Set.Contains(%s) = %v, want %v", key1, got, true)
	}
	if got := len(set); got != 1 {
		t.Errorf("len(Set) = %v, want %v", got, 1)
	}

	// test adding another key
	key2 := "bar"
	set.Add(key2)
	if got := set.Contains(key2); got != true {
		t.Errorf("Set.Contains(%s) = %v, want %v", key2, got, true)
	}
	if got := len(set); got != 2 {
		t.Errorf("len(Set) = %v, want %v", got, 2)
	}
}
