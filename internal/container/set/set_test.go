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
	set := New[string]()
	// test checking a non-existing key
	key1 := "foo"
	if got, want := set.Contains(key1), false; got != want {
		t.Errorf("Set.Contains(%s) = %v, want %v", key1, got, want)
	}
	if got, want := len(set), 0; got != want {
		t.Errorf("len(Set) = %v, want %v", got, want)
	}
	// test adding a new key
	set.Add(key1)
	if got, want := set.Contains(key1), true; got != want {
		t.Errorf("Set.Contains(%s) = %v, want %v", key1, got, want)
	}
	if got, want := len(set), 1; got != want {
		t.Errorf("len(Set) = %v, want %v", got, want)
	}
	// test adding an existing key
	set.Add(key1)
	if got, want := set.Contains(key1), true; got != want {
		t.Errorf("Set.Contains(%s) = %v, want %v", key1, got, want)
	}
	if got, want := len(set), 1; got != want {
		t.Errorf("len(Set) = %v, want %v", got, want)
	}
	// test adding another key
	key2 := "bar"
	set.Add(key2)
	if got, want := set.Contains(key2), true; got != want {
		t.Errorf("Set.Contains(%s) = %v, want %v", key2, got, want)
	}
	if got, want := len(set), 2; got != want {
		t.Errorf("len(Set) = %v, want %v", got, want)
	}
	// test deleting a key
	set.Delete(key1)
	if got, want := set.Contains(key1), false; got != want {
		t.Errorf("Set.Contains(%s) = %v, want %v", key1, got, want)
	}
	if got, want := len(set), 1; got != want {
		t.Errorf("len(Set) = %v, want %v", got, want)
	}
}
