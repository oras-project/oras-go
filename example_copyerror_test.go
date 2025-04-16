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
	"errors"
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

	// Try to copy a non-existent reference, which is expected to fail
	nonExistentRef := "non-existent-reference"
	_, err := oras.Copy(ctx, src, nonExistentRef, dst, "", oras.DefaultCopyOptions)
	if err != nil {
		// Check if the error is a CopyError and print its details
		var copyErr *oras.CopyError
		if errors.As(err, &copyErr) {
			fmt.Println("copyErr.Origin:", copyErr.Origin)
			fmt.Println("copyErr.Op:", copyErr.Op)
			fmt.Println("copyErr.Err:", copyErr.Err)
			fmt.Println("copyErr.Error():", copyErr.Error())
			return
		}

		fmt.Println("err is not a CopyError:", err)
		return
	}

	fmt.Println("Copy succeeded unexpectedly")

	// Output:
	// copyErr.Origin: source
	// copyErr.Op: Resolve
	// copyErr.Err: non-existent-reference: not found
	// copyErr.Error(): failed to perform "Resolve" on source: non-existent-reference: not found
}
