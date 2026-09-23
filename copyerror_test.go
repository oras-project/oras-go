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
	"reflect"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

var errTest error = errors.New("test error")

// testDigest is the digest carried by descTest.
const testDigest = "sha256:6ae8a75555209fd6c44157c0aed8016e763ff435a19cf186f76863140143ff72"

// descTest is a non-zero descriptor used to verify that a CopyError
// identifies the content it failed on.
var descTest = ocispec.Descriptor{
	MediaType: ocispec.MediaTypeImageLayer,
	Digest:    testDigest,
	Size:      12,
}

func TestNewCopyError(t *testing.T) {
	tests := []struct {
		name   string
		op     string
		origin CopyErrorOrigin
		desc   ocispec.Descriptor
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
			name:   "source error with descriptor",
			op:     "Fetch",
			origin: CopyErrorOriginSource,
			desc:   descTest,
			err:    errTest,
			want: &CopyError{
				Op:         "Fetch",
				Origin:     CopyErrorOriginSource,
				Descriptor: descTest,
				Err:        errTest,
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
			name:   "undefined origin",
			op:     "test",
			origin: -1,
			err:    errTest,
			want: &CopyError{
				Op:     "test",
				Origin: -1,
				Err:    errTest,
			},
		},
		{
			name:   "nil error",
			op:     "test",
			origin: CopyErrorOriginSource,
			err:    nil,
			want:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := newCopyError(tt.op, tt.origin, tt.desc, tt.err)
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

			if !reflect.DeepEqual(copyErr.Descriptor, tt.want.Descriptor) {
				t.Errorf("expected Descriptor %v, got %v", tt.want.Descriptor, copyErr.Descriptor)
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
			want: `failed to perform "pull" on source: test error`,
		},
		{
			name: "destination error",
			copyErr: &CopyError{
				Op:     "push",
				Origin: CopyErrorOriginDestination,
				Err:    errTest,
			},
			want: `failed to perform "push" on destination: test error`,
		},
		{
			name: "undefined origin",
			copyErr: &CopyError{
				Op:     "test",
				Origin: -1,
				Err:    errTest,
			},
			want: `failed to perform "test": test error`,
		},
		{
			name: "source error with descriptor",
			copyErr: &CopyError{
				Op:         "Fetch",
				Origin:     CopyErrorOriginSource,
				Descriptor: descTest,
				Err:        errTest,
			},
			want: `failed to perform "Fetch" on source for ` + testDigest + `: test error`,
		},
		{
			name: "destination error with descriptor",
			copyErr: &CopyError{
				Op:         "Push",
				Origin:     CopyErrorOriginDestination,
				Descriptor: descTest,
				Err:        errTest,
			},
			want: `failed to perform "Push" on destination for ` + testDigest + `: test error`,
		},
		{
			name: "undefined origin with descriptor",
			copyErr: &CopyError{
				Op:         "test",
				Origin:     -1,
				Descriptor: descTest,
				Err:        errTest,
			},
			want: `failed to perform "test" for ` + testDigest + `: test error`,
		},
		{
			name: "nil error",
			copyErr: &CopyError{
				Op:     "test",
				Origin: CopyErrorOriginSource,
				Err:    nil,
			},
			want: `failed to perform "test" on source: <nil>`,
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
			name: "undefined origin",
			copyErr: &CopyError{
				Op:     "test",
				Origin: -1,
				Err:    errTest,
			},
			want: errTest,
		},
		{
			name: "nil error",
			copyErr: &CopyError{
				Op:     "test",
				Origin: CopyErrorOriginSource,
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

func TestCopyError_Nested(t *testing.T) {
	msg := "custom error"
	err := &CopyError{
		Op:     "test",
		Origin: CopyErrorOriginSource,
		Err: &customErr{
			Msg: msg,
		},
	}

	var cpErr *CopyError
	if !errors.As(err, &cpErr) {
		t.Fatalf("expected %T, got %T", cpErr, err)
	}

	var ce *customErr
	if !errors.As(err, &ce) {
		t.Fatalf("expected %T, got %T", ce, err)
	}
	if ce.Msg != msg {
		t.Errorf("expected %q, got %q", msg, ce.Msg)
	}
}

type customErr struct {
	Msg string
}

func (e *customErr) Error() string {
	return e.Msg
}

func (e *customErr) Unwrap() error {
	return nil
}

// TestCopyError_Descriptor verifies that a CopyError recovered via errors.As
// exposes the descriptor of the content that the operation failed on, and
// that the zero value leaves the message unchanged.
func TestCopyError_Descriptor(t *testing.T) {
	err := newCopyError("Fetch", CopyErrorOriginSource, descTest, errTest)

	var copyErr *CopyError
	if !errors.As(err, &copyErr) {
		t.Fatalf("expected %T, got %T", copyErr, err)
	}
	if !reflect.DeepEqual(copyErr.Descriptor, descTest) {
		t.Errorf("expected Descriptor %v, got %v", descTest, copyErr.Descriptor)
	}

	zeroErr := newCopyError("Fetch", CopyErrorOriginSource, ocispec.Descriptor{}, errTest)
	if want := `failed to perform "Fetch" on source: test error`; zeroErr.Error() != want {
		t.Errorf("want %q, got %q", want, zeroErr.Error())
	}
}
