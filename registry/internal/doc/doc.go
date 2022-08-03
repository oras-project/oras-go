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

// Package doc provides the constant can be used in example test files. Most of
// the example tests in this repo are fail to run on the GoDoc website because
// of the missing variables. In order to avoid confusing users, these tests
// should be unplayable on the GoDoc website, and this can be achieved by
// leveraging a dot import in the test files.
// Reference: https://pkg.go.dev/go/doc#Example
package doc

// ExampleUnplayable is used in examples test files in order to make example
// tests unplayable.
const ExampleUnplayable = true
