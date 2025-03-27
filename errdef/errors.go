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

package errdef

import (
	"errors"
	"fmt"
)

// Common errors used in ORAS
var (
	ErrAlreadyExists      = errors.New("already exists")
	ErrInvalidDigest      = errors.New("invalid digest")
	ErrInvalidReference   = errors.New("invalid reference")
	ErrInvalidMediaType   = errors.New("invalid media type")
	ErrMissingReference   = errors.New("missing reference")
	ErrNotFound           = errors.New("not found")
	ErrSizeExceedsLimit   = errors.New("size exceeds limit")
	ErrUnsupported        = errors.New("unsupported")
	ErrUnsupportedVersion = errors.New("unsupported version")
)

type CopyErrorOrigin = string

const (
	CopyErrorOriginUnknown     CopyErrorOrigin = "unknown"
	CopyErrorOriginSource      CopyErrorOrigin = "source"
	CopyErrorOriginDestination CopyErrorOrigin = "destination"
)

// TODO: should we put this in the oras package?
type CopyError struct {
	Op      string
	Origin  CopyErrorOrigin
	Err     error
	Message string
}

func NewCopyError(op string, origin CopyErrorOrigin, err error, message string) error {
	switch origin {
	case CopyErrorOriginSource, CopyErrorOriginDestination:
	default:
		origin = CopyErrorOriginUnknown
	}

	return &CopyError{
		Op:      op,
		Origin:  origin,
		Err:     err,
		Message: message,
	}
}

func (e *CopyError) Error() string {
	// what if message is empty?
	switch e.Origin {
	case CopyErrorOriginSource:
		return fmt.Sprintf("error copying from source when performing %s: %s: %v", e.Op, e.Message, e.Err)
	case CopyErrorOriginDestination:
		return fmt.Sprintf("error copying to destination when performing %s: %s: %v", e.Op, e.Message, e.Err)
	default:
		return fmt.Sprintf("error copying when performing %s: %s: %v", e.Op, e.Message, e.Err)
	}
}

func (e *CopyError) Unwrap() error {
	return e.Err
}
