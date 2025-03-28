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

package oras

import "fmt"

// CopyErrorOrigin defines the source of a copy error.
type CopyErrorOrigin string

const (
	// CopyErrorOriginSource indicates the error occurred at the source side.
	CopyErrorOriginSource CopyErrorOrigin = "source"

	// CopyErrorOriginDestination indicates the error occurred at the destination side.
	CopyErrorOriginDestination CopyErrorOrigin = "destination"

	// CopyErrorOriginInternal indicates the error occurred internally.
	CopyErrorOriginInternal CopyErrorOrigin = "internal"
)

// CopyError represents an error encountered during a copy operation.
type CopyError struct {
	Op     string
	Origin CopyErrorOrigin
	Err    error
}

// newCopyError creates a new CopyError.
func newCopyError(op string, origin CopyErrorOrigin, err error) error {
	return &CopyError{
		Op:     op,
		Origin: origin,
		Err:    err,
	}
}

// Error implements the error interface for CopyError.
func (e *CopyError) Error() string {
	return fmt.Sprintf("[%s] failed to perform %s: %v", e.Origin, e.Op, e.Err)
}

// Unwrap implements the errors.Unwrap interface for CopyError.
func (e *CopyError) Unwrap() error {
	return e.Err
}
