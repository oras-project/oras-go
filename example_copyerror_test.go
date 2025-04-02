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

package oras_test

import (
	"context"
	"fmt"

	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/memory"
)

// ExampleCopyError demonstrates how to check CopyError returned from
// copy operations.
func ExampleCopyError() {
	src := memory.New()
	dst := memory.New()
	ctx := context.Background()

	// Try to copy a non-existent reference
	nonExistentRef := "non-existent-reference"
	_, err := oras.Copy(ctx, src, nonExistentRef, dst, "", oras.DefaultCopyOptions)
	if err == nil {
		fmt.Println("copy succeeded")
		return
	}

	// Type assertion to check if it's a CopyError
	copyErr, ok := err.(*oras.CopyError)
	if !ok {
		fmt.Println("err is not a CopyError")
		return
	}

	fmt.Println("copyErr.Origin:", copyErr.Origin)
	fmt.Println("copyErr.Op:", copyErr.Op)
	fmt.Println("copyErr.Err:", copyErr.Err)
	fmt.Println("copyErr.Error():", copyErr.Error())

	// Output:
	// copyErr.Origin: source
	// copyErr.Op: resolveRoot
	// copyErr.Err: not found
	// copyErr.Error(): source error: failed to perform "resolveRoot": not found
}
