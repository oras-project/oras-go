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

package credentials

import (
	"fmt"
	"os"
	"testing"
)

// TestMain guards the implicit dependency that the tests in this package have
// on a registered config loader.
//
// Many tests here exercise NewStore and NewStoreFromDocker, which fail with
// ErrNoConfigLoader unless something has called SetDefaultConfigLoader. Nothing
// in this package can do that: the config package imports credentials, so
// importing config from package credentials would be an import cycle. The
// registration instead comes from the blank import of config in
// file_store_test.go, which is in the external credentials_test package and can
// therefore close the cycle. Both packages are linked into the same test
// binary, so their init functions run before this TestMain.
//
// That dependency spans two packages and is easy to break by accident, so fail
// once with an explanation here rather than letting dozens of tests fail with
// an unexplained ErrNoConfigLoader.
func TestMain(m *testing.M) {
	if defaultConfigLoader == nil {
		fmt.Fprintln(os.Stderr, "no config loader registered: the tests in package credentials "+
			"require the blank import of registry/remote/config in file_store_test.go "+
			"(package credentials_test). Restore it, or register a loader with "+
			"SetDefaultConfigLoader.")
		os.Exit(1)
	}
	os.Exit(m.Run())
}
