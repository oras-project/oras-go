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

package content

import (
	"bytes"
	"crypto/rand"
	_ "crypto/sha256"
	"io"
	"reflect"
	"testing"

	"github.com/opencontainers/go-digest"
)

// Copied from https://github.com/opencontainers/go-digest/blob/master/digest.go

// FromBytes digests the input and returns a Digest.
func FromBytes(p []byte) digest.Digest {
	return digest.Canonical.FromBytes(p)
}

// Copied from https://github.com/opencontainers/go-digest/blob/master/verifiers_test.go

func TestDigestVerifier(t *testing.T) {
	p := make([]byte, 1<<20)
	rand.Read(p)
	content := FromBytes(p)

	verifier := content.Verifier()

	io.Copy(verifier, bytes.NewReader(p))

	if !verifier.Verified() {
		t.Fatalf("bytes not verified")
	}
}

// TestVerifierUnsupportedDigest ensures that unsupported digest validation is
// flowing through verifier creation.
func TestVerifierUnsupportedDigest(t *testing.T) {
	for _, testcase := range []struct {
		Name     string
		Digest   digest.Digest
		Expected interface{} // expected panic target
	}{
		{
			Name:     "Empty",
			Digest:   "",
			Expected: "no ':' separator in digest \"\"",
		},
		{
			Name:     "EmptyAlg",
			Digest:   ":",
			Expected: "empty digest algorithm, validate before calling Algorithm.Hash()",
		},
		{
			Name:     "Unsupported",
			Digest:   digest.Digest("bean:0123456789abcdef"),
			Expected: "bean not available (make sure it is imported)",
		},
		{
			Name:     "Garbage",
			Digest:   digest.Digest("sha256-garbage:pure"),
			Expected: "sha256-garbage not available (make sure it is imported)",
		},
	} {
		t.Run(testcase.Name, func(t *testing.T) {
			expected := testcase.Expected
			defer func() {
				recovered := recover()
				if !reflect.DeepEqual(recovered, expected) {
					t.Fatalf("unexpected recover: %v != %v", recovered, expected)
				}
			}()

			_ = testcase.Digest.Verifier()
		})
	}
}
