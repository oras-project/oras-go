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

import (
	"errors"
	"testing"
)

var errTest error = errors.New("test error")

func TestNewCopyError(t *testing.T) {
	tests := []struct {
		name   string
		op     string
		origin CopyErrorOrigin
		err    error
		want   *CopyError
	}{
		{
			name:   "source error",
			op:     "pull",
			origin: CopyErrorOriginSource,
			err:    errTest,
			want: &CopyError{
				Op:     "pull",
				Origin: CopyErrorOriginSource,
				Err:    errTest,
			},
		},
		{
			name:   "destination error",
			op:     "push",
			origin: CopyErrorOriginDestination,
			err:    errTest,
			want: &CopyError{
				Op:     "push",
				Origin: CopyErrorOriginDestination,
				Err:    errTest,
			},
		},
		{
			name:   "internal error",
			op:     "copy",
			origin: CopyErrorOriginInternal,
			err:    errTest,
			want: &CopyError{
				Op:     "copy",
				Origin: CopyErrorOriginInternal,
				Err:    errTest,
			},
		},
		{
			name:   "undefined origin",
			op:     "test",
			origin: "somewhere",
			err:    errTest,
			want: &CopyError{
				Op:     "test",
				Origin: "somewhere",
				Err:    errTest,
			},
		},
		{
			name:   "nil error",
			op:     "test",
			origin: CopyErrorOriginInternal,
			err:    nil,
			want:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := newCopyError(tt.op, tt.origin, tt.err)
			if tt.want == nil {
				return
			}
			copyErr, ok := err.(*CopyError)
			if !ok {
				t.Fatalf("expected err to be *CopyError, got %T", err)
			}

			if copyErr.Op != tt.want.Op {
				t.Errorf("expected Op %q, got %q", tt.want.Op, copyErr.Op)
			}

			if copyErr.Origin != tt.want.Origin {
				t.Errorf("expected Origin %q, got %q", tt.want.Origin, copyErr.Origin)
			}

			if !errors.Is(copyErr.Err, errTest) {
				t.Errorf("expected Err %q, got %q", tt.want.Err, copyErr.Err)
			}
		})
	}
}

func TestCopyError_Error(t *testing.T) {
	tests := []struct {
		name    string
		copyErr *CopyError
		want    string
	}{
		{
			name: "source error",
			copyErr: &CopyError{
				Op:     "pull",
				Origin: CopyErrorOriginSource,
				Err:    errTest,
			},
			want: `source error: failed to perform "pull": test error`,
		},
		{
			name: "destination error",
			copyErr: &CopyError{
				Op:     "push",
				Origin: CopyErrorOriginDestination,
				Err:    errTest,
			},
			want: `destination error: failed to perform "push": test error`,
		},
		{
			name: "internal error",
			copyErr: &CopyError{
				Op:     "copy",
				Origin: CopyErrorOriginInternal,
				Err:    errTest,
			},
			want: `internal error: failed to perform "copy": test error`,
		},
		{
			name: "undefined origin",
			copyErr: &CopyError{
				Op:     "test",
				Origin: "somewhere",
				Err:    errTest,
			},
			want: `somewhere error: failed to perform "test": test error`,
		},
		{
			name: "nil error",
			copyErr: &CopyError{
				Op:     "test",
				Origin: CopyErrorOriginInternal,
				Err:    nil,
			},
			want: `internal error: failed to perform "test": <nil>`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if errStr := tt.copyErr.Error(); errStr != tt.want {
				t.Errorf("want %q, got %q", tt.want, errStr)
			}
		})
	}
}

func TestCopyError_Unwrap(t *testing.T) {
	tests := []struct {
		name    string
		copyErr *CopyError
		want    error
	}{
		{
			name: "unwrap source error",
			copyErr: &CopyError{
				Op:     "pull",
				Origin: CopyErrorOriginSource,
				Err:    errTest,
			},
			want: errTest,
		},
		{
			name: "unwrap destination error",
			copyErr: &CopyError{
				Op:     "push",
				Origin: CopyErrorOriginDestination,
				Err:    errTest,
			},
			want: errTest,
		},
		{
			name: "unwrap internal error",
			copyErr: &CopyError{
				Op:     "copy",
				Origin: CopyErrorOriginInternal,
				Err:    errTest,
			},
			want: errTest,
		},
		{
			name: "undefined origin",
			copyErr: &CopyError{
				Op:     "test",
				Origin: "somewhere",
				Err:    errTest,
			},
			want: errTest,
		},
		{
			name: "nil error",
			copyErr: &CopyError{
				Op:     "test",
				Origin: CopyErrorOriginInternal,
				Err:    nil,
			},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.copyErr.Unwrap(); err != tt.want {
				t.Errorf("want %v, got %v", tt.want, err)
			}
		})
	}
}
