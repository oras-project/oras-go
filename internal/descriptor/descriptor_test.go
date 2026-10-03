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

package descriptor

import (
	"testing"

	"github.com/opencontainers/go-digest"
)

func TestIsSupportedAlgorithm(t *testing.T) {
	tests := []struct {
		alg  digest.Algorithm
		want bool
	}{
		{digest.SHA256, true},
		{digest.SHA384, true},
		{digest.SHA512, true},
		{digest.Algorithm("blake3"), false},
		{digest.Algorithm("sha1"), false},
		{digest.Algorithm("SHA256"), false},
		{digest.Algorithm(""), false},
	}
	for _, tt := range tests {
		t.Run(string(tt.alg), func(t *testing.T) {
			if got := IsSupportedAlgorithm(tt.alg); got != tt.want {
				t.Errorf("IsSupportedAlgorithm(%q) = %v, want %v", tt.alg, got, tt.want)
			}
		})
	}
}
