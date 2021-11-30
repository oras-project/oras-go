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
package ioutil

import (
	"reflect"
	"strings"
	"testing"
)

func TestUnwrapReader(t *testing.T) {
	base := strings.NewReader("test")
	r := NopCloser(base)

	if got := UnwrapReader(r); !reflect.DeepEqual(got, base) {
		t.Errorf("UnwrapReader() = %v, want %v", got, base)
	}

	// wrap again
	r = NopCloser(r)
	if got := UnwrapReader(r); !reflect.DeepEqual(got, base) {
		t.Errorf("UnwrapReader() = %v, want %v", got, base)
	}
}

func TestNopCloserInterface(t *testing.T) {
	if _, ok := NopCloser(nil).(ReaderUnwrapper); !ok {
		t.Error("nopCloser{} does not conform ReaderUnwrapper")
	}
}
