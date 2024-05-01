/*
Copyright 2019, 2020 OCI Contributors
Copyright 2017 Docker, Inc.
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
	"hash"

	"github.com/opencontainers/go-digest"
)

// Verifier returns a writer object that can be used to verify a stream of
// content against the digest. If the digest is invalid, the method will panic.
func Verifier(d digest.Digest) digest.Verifier {
	return hashVerifier{
		hash:   d.Algorithm().Hash(),
		digest: d,
	}
}

// Copied from https://github.com/opencontainers/go-digest/blob/master/verifiers.go
// Since hashVerifier is non-public in go-digest and we need to supply a pre-initialized
// Hash we'll just make our own. Thank you Verifier interface!

type hashVerifier struct {
	digest digest.Digest
	hash   hash.Hash
}

func (hv hashVerifier) Write(p []byte) (n int, err error) {
	return hv.hash.Write(p)
}

func (hv hashVerifier) Verified() bool {
	return hv.digest == digest.NewDigest(hv.digest.Algorithm(), hv.hash)
}
