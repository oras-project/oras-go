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

// Package registry_test gives examples code of functions in the registry package.
package registry_test

import (
	"context"
	"fmt"

	"oras.land/oras-go/v2/registry"
	"oras.land/oras-go/v2/registry/remote"
)

// ExampleRepositories gives example snippets for listing respositories in the registry without pagination.
func ExampleRepositories() {
	reg, err := remote.NewRegistry(host)
	if err != nil {
		panic(err) // Handle error
	}

	ctx := context.Background()
	repos, err := registry.Repositories(ctx, reg)
	if err != nil {
		panic(err) // Handle error
	}
	for _, repo := range repos {
		fmt.Println(repo)
	}
	// Output:
	// public/repo1
	// public/repo2
	// internal/repo3
}
