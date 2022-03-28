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

// Package registry_test gives examples code of functions in the remote package.
package remote_test

import (
	"context"
	"fmt"

	"oras.land/oras-go/v2/registry/remote"
)

// var host string

// func TestMain(m *testing.M) {
// 	// Setup mocked registries
// 	httpsServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
// 		result := struct {
// 			Repositories []string `json:"repositories"`
// 		}{
// 			Repositories: []string{"public/repo1", "public/repo2", "internal/repo3"},
// 		}
// 		json.NewEncoder(w).Encode(result)
// 	}))
// 	defer httpsServer.Close()
// 	u, err := url.Parse(httpsServer.URL)
// 	if err != nil {
// 		panic(err)
// 	}
// 	host = u.Host
// 	http.DefaultClient = httpsServer.Client()
// 	os.Exit(m.Run())
// }

// ExampleRegistry_Repositories gives example snippets for listing respositories in a HTTPS registry with pagination.
func ExampleRegistry_Repositories() {
	reg, err := remote.NewRegistry(host)
	if err != nil {
		panic(err) // Handle error
	}
	// If you want to play with your local registry, try to override
	// the `host` variable. Don't forget to set HTTP option as below:
	// reg.PlainHTTP = true

	ctx := context.Background()
	err = reg.Repositories(ctx, func(repos []string) error {
		for _, repo := range repos {
			fmt.Println(repo)
		}
		return nil
	})
	if err != nil {
		panic(err) // Handle error
	}
	// Output:
	// public/repo1
	// public/repo2
	// internal/repo3
}
