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

package trace_test

import (
	"context"
	"fmt"

	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/credentials"
	"oras.land/oras-go/v2/registry/remote/credentials/trace"
)

// An example on how to use ExecutableTrace with Stores.
func Example() {
	// ExecutableTrace works with all Stores that may invoke executables, for
	// example the Store returned from NewStore and NewNativeStore.
	store, err := credentials.NewStore("example/path/config.json", credentials.StoreOptions{})
	if err != nil {
		panic(err)
	}

	// Define ExecutableTrace and add it to the context. The 'action' argument
	// refers to one of 'store', 'get' and 'erase' defined by the docker
	// credential helper protocol.
	// Reference: https://docs.docker.com/engine/reference/commandline/login/#credential-helper-protocol
	traceHooks := &trace.ExecutableTrace{
		ExecuteStart: func(executableName string, action string) {
			fmt.Printf("executable %s, action %s started", executableName, action)
		},
		ExecuteDone: func(executableName string, action string, err error) {
			fmt.Printf("executable %s, action %s finished", executableName, action)
		},
	}
	ctx := trace.WithExecutableTrace(context.Background(), traceHooks)

	// Get, Put and Delete credentials from store. If any credential helper
	// executable is run, traceHooks is executed.
	err = store.Put(ctx, "localhost:5000", auth.Credential{Username: "testUsername", Password: "testPassword"})
	if err != nil {
		panic(err)
	}

	cred, err := store.Get(ctx, "localhost:5000")
	if err != nil {
		panic(err)
	}
	fmt.Println(cred)

	err = store.Delete(ctx, "localhost:5000")
	if err != nil {
		panic(err)
	}
}
