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
package syncutil

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestOnce_Do(t *testing.T) {
	var f []func() (interface{}, error)
	for i := 0; i < 100; i++ {
		f = append(f, func(i int) func() (interface{}, error) {
			return func() (interface{}, error) {
				return i + 1, errors.New(strconv.Itoa(i))
			}
		}(i))
	}

	once := NewOnce()
	first := make([]bool, len(f))
	result := make([]interface{}, len(f))
	err := make([]error, len(f))
	var wg sync.WaitGroup
	for i := 0; i < len(f); i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx := context.Background()
			first[i], result[i], err[i] = once.Do(ctx, f[i])
		}(i)
	}
	wg.Wait()

	target := 0
	for i := 0; i < len(f); i++ {
		if first[i] {
			target = i
			break
		}
	}
	targetErr := err[target]
	if targetErr == nil || targetErr.Error() != strconv.Itoa(target) {
		t.Errorf("Once.Do(%d) error = %v, wantErr %v", target, targetErr, strconv.Itoa(target))
	}

	wantResult := target + 1
	wantErr := targetErr
	for i := 0; i < len(f); i++ {
		wantFirst := false
		if i == target {
			wantFirst = true
		}
		if first[i] != wantFirst {
			t.Errorf("Once.Do(%d) first = %v, want %v", i, first[i], wantFirst)
		}
		if err[i] != wantErr {
			t.Errorf("Once.Do(%d) error = %v, wantErr %v", i, err[i], wantErr)
		}
		if !reflect.DeepEqual(result[i], wantResult) {
			t.Errorf("Once.Do(%d) result = %v, want %v", i, result[i], wantResult)
		}
	}
}

func TestOnce_Do_Cancel_Context(t *testing.T) {
	once := NewOnce()

	var wg sync.WaitGroup
	var (
		first  bool
		result interface{}
		err    error
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		ctx := context.Background()
		first, result, err = once.Do(ctx, func() (interface{}, error) {
			time.Sleep(200 * time.Millisecond)
			return "foo", io.EOF
		})
	}()
	time.Sleep(100 * time.Millisecond)
	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	first2, result2, err2 := once.Do(ctx, func() (interface{}, error) {
		return "bar", nil
	})
	wg.Wait()

	if wantFirst := true; first != wantFirst {
		t.Fatalf("Once.Do() first = %v, want %v", first, wantFirst)
	}
	if wantErr := io.EOF; err != wantErr {
		t.Fatalf("Once.Do() error = %v, wantErr %v", err, wantErr)
	}
	if wantResult := "foo"; !reflect.DeepEqual(result, wantResult) {
		t.Fatalf("Once.Do() result = %v, want %v", result, wantResult)
	}

	if wantFirst := false; first2 != wantFirst {
		t.Fatalf("Once.Do() first = %v, want %v", first2, wantFirst)
	}
	if wantErr := context.Canceled; err2 != wantErr {
		t.Fatalf("Once.Do() error = %v, wantErr %v", err2, wantErr)
	}
	if wantResult := interface{}(nil); !reflect.DeepEqual(result2, wantResult) {
		t.Fatalf("Once.Do() result = %v, want %v", result2, wantResult)
	}
}

func TestOnce_Do_Cancel_Function(t *testing.T) {
	ctx := context.Background()
	once := NewOnce()

	first, result, err := once.Do(ctx, func() (interface{}, error) {
		return "foo", context.Canceled
	})
	if wantFirst := false; first != wantFirst {
		t.Fatalf("Once.Do() first = %v, want %v", first, wantFirst)
	}
	if wantErr := context.Canceled; err != wantErr {
		t.Fatalf("Once.Do() error = %v, wantErr %v", err, wantErr)
	}
	if wantResult := interface{}(nil); !reflect.DeepEqual(result, wantResult) {
		t.Fatalf("Once.Do() result = %v, want %v", result, wantResult)
	}

	first, result, err = once.Do(ctx, func() (interface{}, error) {
		return "bar", io.EOF
	})
	if wantFirst := true; first != wantFirst {
		t.Fatalf("Once.Do() first = %v, want %v", first, wantFirst)
	}
	if wantErr := io.EOF; err != wantErr {
		t.Fatalf("Once.Do() error = %v, wantErr %v", err, wantErr)
	}
	if wantResult := "bar"; !reflect.DeepEqual(result, wantResult) {
		t.Fatalf("Once.Do() result = %v, want %v", result, wantResult)
	}
}

func TestOnce_Do_Cancel_Panic(t *testing.T) {
	ctx := context.Background()
	once := NewOnce()

	func() {
		defer func() {
			got := recover()
			want := "foo"
			if got != want {
				t.Fatalf("Once.Do() panic = %v, want %v", got, want)
			}
		}()
		once.Do(ctx, func() (interface{}, error) {
			panic("foo")
		})
	}()

	first, result, err := once.Do(ctx, func() (interface{}, error) {
		return "bar", io.EOF
	})
	if wantFirst := true; first != wantFirst {
		t.Fatalf("Once.Do() first = %v, want %v", first, wantFirst)
	}
	if wantErr := io.EOF; err != wantErr {
		t.Fatalf("Once.Do() error = %v, wantErr %v", err, wantErr)
	}
	if wantResult := "bar"; !reflect.DeepEqual(result, wantResult) {
		t.Fatalf("Once.Do() result = %v, want %v", result, wantResult)
	}
}
