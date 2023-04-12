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

package remote

import (
	"fmt"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// IgnorableError implements error but can be ignored on caller side.
type IgnorableError struct {
	Op  string
	Err error
	ocispec.Descriptor
}

// Error returns error msg of IgnorableError.
func (e *IgnorableError) Error() string {
	return fmt.Sprintf("failed to %s: %s", e.Op, e.Err.Error())
}

// Unwrap returns the inner error of IgnorableError.
func (e *IgnorableError) Unwrap() error {
	return e.Err
}
