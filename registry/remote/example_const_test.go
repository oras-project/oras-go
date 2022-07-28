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
// Package remote_test includes all the testable examples for remote repository type

package remote_test

const (
	exampleRepositoryName   = "example"
	exampleTag              = "latest"
	exampleManifest         = "Example manifest content"
	exampleLayer            = "Example layer content"
	exampleUploadUUid       = "0bc84d80-837c-41d9-824e-1907463c53b3"
	ManifestDigest          = "sha256:0b696106ecd0654e031f19e0a8cbd1aee4ad457d7c9cea881f07b12a930cd307"
	ReferenceManifestDigest = "sha256:b2122d3fd728173dd6b68a0b73caa129302b78c78273ba43ead541a88169c855"
)

var host string
