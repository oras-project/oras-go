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

// ExampleCopyError demonstrates how to check CopyError objects returned from
// copy operations.
func ExampleCopyError() {
	src := memory.New()
	dst := memory.New()
	ctx := context.Background()

	// Try to copy a non-existent reference
	nonExistentRef := "non-existent-reference"
	_, err := oras.Copy(ctx, src, nonExistentRef, dst, "", oras.DefaultCopyOptions)
	if err == nil {
		fmt.Println("Copy succeeded")
		return
	}

	// Type assertion to check if it's a CopyError
	copyErr, ok := err.(*oras.CopyError)
	if !ok {
		fmt.Printf("Unexpected error type: %T\n", err)
		return
	}

	fmt.Println("CopyErr.Origin:", copyErr.Origin)
	fmt.Println("CopyErr.Op:", copyErr.Op)
	fmt.Println("CopyErr.Err:", copyErr.Err)
	fmt.Println("CopyErr.Error():", copyErr.Error())

	// Output:
	// CopyErr.Origin: source
	// CopyErr.Op: resolveRoot
	// CopyErr.Err: not found
	// CopyErr.Error(): source error: failed to perform "resolveRoot": not found
}
