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

package credentials_test

import (
	"context"
	"fmt"
	"path/filepath"

	"oras.land/oras-go/v2/registry/remote/credentials"
)

func ExampleNewStoreFromDocker() {
	// Create a store using a custom Docker config.json file
	configPath := filepath.Join("testdata", "example_docker_config.json")
	store, err := credentials.NewStoreFromDocker(credentials.StoreOptions{
		ConfigurationPath: configPath,
		AllowPlaintextPut: true,
	})
	if err != nil {
		panic(err)
	}

	ctx := context.Background()

	// Retrieve existing credentials from the config file
	cred, err := store.Get(ctx, "docker.io")
	if err != nil {
		panic(err)
	}
	fmt.Printf("Username: %s\n", cred.Username)

	// Store new credentials for another registry
	err = store.Put(ctx, "registry-1.docker.io", credentials.Credential{
		Username: "newuser",
		Password: "newpassword",
	})
	if err != nil {
		panic(err)
	}

	// Retrieve the newly stored credentials
	newCred, err := store.Get(ctx, "registry-1.docker.io")
	if err != nil {
		panic(err)
	}
	fmt.Printf("New Username: %s\n", newCred.Username)

	// Clean up the new entry
	err = store.Delete(ctx, "registry-1.docker.io")
	if err != nil {
		panic(err)
	}
	fmt.Println("Docker config store example completed")

	// Output:
	// Username: myusername
	// New Username: newuser
	// Docker config store example completed
}

func ExampleNewStoreFromRegistriesConf() {
	// Create a store using a custom registries.conf file
	configPath := filepath.Join("testdata", "example_registries.conf")
	store, err := credentials.NewStoreFromRegistriesConf(credentials.StoreOptions{
		ConfigurationPath: configPath,
		AllowPlaintextPut: true,
	})
	if err != nil {
		panic(err)
	}

	ctx := context.Background()

	// Store credentials for a registry
	err = store.Put(ctx, "quay.io", credentials.Credential{
		Username: "myusername",
		Password: "mytoken",
	})
	if err != nil {
		panic(err)
	}

	// Retrieve credentials from the store
	cred, err := store.Get(ctx, "quay.io")
	if err != nil {
		panic(err)
	}
	fmt.Printf("Username: %s\n", cred.Username)

	// Store additional credentials
	err = store.Put(ctx, "gcr.io", credentials.Credential{
		Username: "_token",
		Password: "gcrtoken",
	})
	if err != nil {
		panic(err)
	}

	// Retrieve the additional credentials
	gcrCred, err := store.Get(ctx, "gcr.io")
	if err != nil {
		panic(err)
	}
	fmt.Printf("GCR Username: %s\n", gcrCred.Username)

	// Clean up
	err = store.Delete(ctx, "quay.io")
	if err != nil {
		panic(err)
	}
	err = store.Delete(ctx, "gcr.io")
	if err != nil {
		panic(err)
	}
	fmt.Println("Registries.conf store example completed")

	// Output:
	// Username: myusername
	// GCR Username: _token
	// Registries.conf store example completed
}

func ExampleNewStoreWithFallbacks_dockerPrimaryRegistriesConfFallback() {
	// Create primary store using Docker config.json
	dockerConfigPath := filepath.Join("testdata", "example_docker_config.json")
	dockerStore, err := credentials.NewStoreFromDocker(credentials.StoreOptions{
		ConfigurationPath: dockerConfigPath,
		AllowPlaintextPut: true,
	})
	if err != nil {
		panic(err)
	}

	// Create fallback store using registries.conf
	registriesConfigPath := filepath.Join("testdata", "example_registries.conf")
	registriesStore, err := credentials.NewStoreFromRegistriesConf(credentials.StoreOptions{
		ConfigurationPath: registriesConfigPath,
		AllowPlaintextPut: true,
	})
	if err != nil {
		panic(err)
	}

	// Combine stores with Docker as primary and registries.conf as fallback
	store := credentials.NewStoreWithFallbacks(dockerStore, registriesStore)

	ctx := context.Background()

	// Get credentials from Docker config (should find docker.io)
	dockerCred, err := store.Get(ctx, "docker.io")
	if err != nil {
		panic(err)
	}
	fmt.Printf("Docker.io Username: %s\n", dockerCred.Username)

	// Store credentials (will go to primary store - Docker config)
	err = store.Put(ctx, "gcr.io", credentials.Credential{
		Username: "_token",
		Password: "myaccesstoken",
	})
	if err != nil {
		panic(err)
	}

	// Retrieve credentials (will find in primary Docker store)
	cred, err := store.Get(ctx, "gcr.io")
	if err != nil {
		panic(err)
	}
	fmt.Printf("GCR Username: %s\n", cred.Username)

	// Clean up
	err = store.Delete(ctx, "gcr.io")
	if err != nil {
		panic(err)
	}
	fmt.Println("Store with fallbacks example completed")

	// Output:
	// Docker.io Username: myusername
	// GCR Username: _token
	// Store with fallbacks example completed
}
