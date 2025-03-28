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

type CopyErrorOrigin = string

const (
	CopyErrorOriginUnknown     CopyErrorOrigin = "unknown"
	CopyErrorOriginSource      CopyErrorOrigin = "source"
	CopyErrorOriginDestination CopyErrorOrigin = "destination"
)

type CopyError struct {
	Op     string
	Origin CopyErrorOrigin
	Err    error
}

func NewCopyError(op string, origin CopyErrorOrigin, err error) error {
	switch origin {
	case CopyErrorOriginSource, CopyErrorOriginDestination:
	default:
		// TODO: should we do this?
		origin = CopyErrorOriginUnknown
	}

	return &CopyError{
		Op:     op,
		Origin: origin,
		Err:    err,
	}
}

func (e *CopyError) Error() string {
	switch e.Origin {
	case CopyErrorOriginSource, CopyErrorOriginDestination:
		return fmt.Sprintf("error copying when performing %s on %s target: %v", e.Op, e.Origin, e.Err)
	default:
		return fmt.Sprintf("error copying when performing %s: %v", e.Op, e.Err)
	}
}

func (e *CopyError) Unwrap() error {
	return e.Err
}
