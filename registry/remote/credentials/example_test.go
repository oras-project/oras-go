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
	"net/http"

	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	credentials "oras.land/oras-go/v2/registry/remote/credentials"
)

func ExampleNewNativeStore() {
	ns := credentials.NewNativeStore("pass")

	ctx := context.Background()
	// save credentials into the store
	err := ns.Put(ctx, "localhost:5000", auth.Credential{
		Username: "username-example",
		Password: "password-example",
	})
	if err != nil {
		panic(err)
	}

	// get credentials from the store
	cred, err := ns.Get(ctx, "localhost:5000")
	if err != nil {
		panic(err)
	}
	fmt.Println(cred)

	// delete the credentials from the store
	err = ns.Delete(ctx, "localhost:5000")
	if err != nil {
		panic(err)
	}
}

func ExampleNewFileStore() {
	fs, err := credentials.NewFileStore("example/path/config.json")
	if err != nil {
		panic(err)
	}

	ctx := context.Background()
	// save credentials into the store
	err = fs.Put(ctx, "localhost:5000", auth.Credential{
		Username: "username-example",
		Password: "password-example",
	})
	if err != nil {
		panic(err)
	}

	// get credentials from the store
	cred, err := fs.Get(ctx, "localhost:5000")
	if err != nil {
		panic(err)
	}
	fmt.Println(cred)

	// delete the credentials from the store
	err = fs.Delete(ctx, "localhost:5000")
	if err != nil {
		panic(err)
	}
}

func ExampleNewStore() {
	// NewStore returns a Store based on the given configuration file. It will
	// automatically determine which Store (file store or native store) to use.
	// If the native store is not available, you can save your credentials in
	// the configuration file by specifying AllowPlaintextPut: true, but keep
	// in mind that this is an unsafe workaround.
	// See the documentation for details.
	store, err := credentials.NewStore("example/path/config.json", credentials.StoreOptions{
		AllowPlaintextPut: true,
	})
	if err != nil {
		panic(err)
	}

	ctx := context.Background()
	// save credentials into the store
	err = store.Put(ctx, "localhost:5000", auth.Credential{
		Username: "username-example",
		Password: "password-example",
	})
	if err != nil {
		panic(err)
	}

	// get credentials from the store
	cred, err := store.Get(ctx, "localhost:5000")
	if err != nil {
		panic(err)
	}
	fmt.Println(cred)

	// delete the credentials from the store
	err = store.Delete(ctx, "localhost:5000")
	if err != nil {
		panic(err)
	}
}

func ExampleNewStoreFromDocker() {
	ds, err := credentials.NewStoreFromDocker(credentials.StoreOptions{
		AllowPlaintextPut: true,
	})
	if err != nil {
		panic(err)
	}

	ctx := context.Background()
	// save credentials into the store
	err = ds.Put(ctx, "localhost:5000", auth.Credential{
		Username: "username-example",
		Password: "password-example",
	})
	if err != nil {
		panic(err)
	}

	// get credentials from the store
	cred, err := ds.Get(ctx, "localhost:5000")
	if err != nil {
		panic(err)
	}
	fmt.Println(cred)

	// delete the credentials from the store
	err = ds.Delete(ctx, "localhost:5000")
	if err != nil {
		panic(err)
	}
}

func ExampleNewStoreWithFallbacks_configAsPrimaryStoreDockerAsFallback() {
	primaryStore, err := credentials.NewStore("example/path/config.json", credentials.StoreOptions{
		AllowPlaintextPut: true,
	})
	if err != nil {
		panic(err)
	}
	fallbackStore, err := credentials.NewStoreFromDocker(credentials.StoreOptions{})
	sf := credentials.NewStoreWithFallbacks(primaryStore, fallbackStore)

	ctx := context.Background()
	// save credentials into the store
	err = sf.Put(ctx, "localhost:5000", auth.Credential{
		Username: "username-example",
		Password: "password-example",
	})
	if err != nil {
		panic(err)
	}

	// get credentials from the store
	cred, err := sf.Get(ctx, "localhost:5000")
	if err != nil {
		panic(err)
	}
	fmt.Println(cred)

	// delete the credentials from the store
	err = sf.Delete(ctx, "localhost:5000")
	if err != nil {
		panic(err)
	}
}

func ExampleLogin() {
	store, err := credentials.NewStore("example/path/config.json", credentials.StoreOptions{
		AllowPlaintextPut: true,
	})
	if err != nil {
		panic(err)
	}
	registry, err := remote.NewRegistry("localhost:5000")
	if err != nil {
		panic(err)
	}
	cred := auth.Credential{
		Username: "username-example",
		Password: "password-example",
	}
	err = credentials.Login(context.Background(), store, registry, cred)
	if err != nil {
		panic(err)
	}
	fmt.Println("Login succeeded")
}

func ExampleLogout() {
	store, err := credentials.NewStore("example/path/config.json", credentials.StoreOptions{})
	if err != nil {
		panic(err)
	}
	err = credentials.Logout(context.Background(), store, "localhost:5000")
	if err != nil {
		panic(err)
	}
	fmt.Println("Logout succeeded")
}

func ExampleCredential() {
	store, err := credentials.NewStore("example/path/config.json", credentials.StoreOptions{})
	if err != nil {
		panic(err)
	}

	client := auth.DefaultClient
	client.Credential = credentials.Credential(store)

	request, err := http.NewRequest(http.MethodGet, "localhost:5000", nil)
	if err != nil {
		panic(err)
	}

	_, err = client.Do(request)
	if err != nil {
		panic(err)
	}
}
