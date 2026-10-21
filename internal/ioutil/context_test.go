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

package ioutil

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestNewContextReader(t *testing.T) {
	const input = "hello world"
	got, err := io.ReadAll(NewContextReader(context.Background(), strings.NewReader(input)))
	if err != nil || string(got) != input {
		t.Fatalf("ReadAll() = %q, %v, want %q, nil", got, err, input)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	expired, cancelDeadline := context.WithDeadline(context.Background(), time.Unix(0, 0))
	defer cancelDeadline()
	fail := errors.New("read failed")
	for _, tt := range []struct {
		name               string
		ctx                context.Context
		sourceErr, wantErr error
		wantN, wantCalls   int
		cancelAfterFirst   bool
	}{
		{"canceled", canceled, nil, context.Canceled, 0, 0, false},
		{"deadline", expired, nil, context.DeadlineExceeded, 0, 0, false},
		{"between reads", context.Background(), nil, nil, 1, 1, true},
		{"EOF", context.Background(), io.EOF, io.EOF, 1, 1, false},
		{"error", context.Background(), fail, fail, 1, 1, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(tt.ctx)
			defer cancel()
			calls := 0
			r := NewContextReader(ctx, contextReadFunc(func(p []byte) (int, error) {
				calls++
				return copy(p, "a"), tt.sourceErr
			}))
			p := make([]byte, 1)
			n, err := r.Read(p)
			if n != tt.wantN || err != tt.wantErr || (tt.wantN == 1 && string(p) != "a") {
				t.Errorf("Read() = %d, %v, %q, want %d, %v, a when read", n, err, p, tt.wantN, tt.wantErr)
			}
			if tt.cancelAfterFirst {
				cancel()
				if n, err := r.Read(p); n != 0 || err != context.Canceled {
					t.Errorf("next Read() = %d, %v, want 0, context.Canceled", n, err)
				}
			}
			if calls != tt.wantCalls {
				t.Errorf("underlying reader called %d times, want %d", calls, tt.wantCalls)
			}
		})
	}
}

func TestCopyUnsafe_CanceledDuringCopy(t *testing.T) {
	for _, size := range []int64{-1, 1} {
		ctx, cancel := context.WithCancel(context.Background())
		r := NewContextReader(ctx, contextReadFunc(func(p []byte) (int, error) {
			cancel()
			return copy(p, "a"), nil
		}))
		// Known size exercises the trailing-byte probe after the last data Read.
		got, err := CopyUnsafe(io.Discard, r, nil, ocispec.Descriptor{Size: size}, 8)
		cancel()
		if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(got, ocispec.Descriptor{}) {
			t.Errorf("CopyUnsafe(size=%d) = %+v, %v, want empty, context.Canceled", size, got, err)
		}
	}
}

type contextReadFunc func([]byte) (int, error)

func (f contextReadFunc) Read(p []byte) (int, error) { return f(p) }
