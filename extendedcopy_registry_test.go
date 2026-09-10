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
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	oras "github.com/oras-project/oras-go/v3"
	"github.com/oras-project/oras-go/v3/registry/remote"
)

func TestRegistryRepositorySupportsExtendedCopyGraph(t *testing.T) {
	ctx := context.Background()

	reg, err := remote.NewRegistry("example.com")
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	repo, err := reg.Repository(ctx, "test")
	if err != nil {
		t.Fatalf("Repository() error = %v", err)
	}

	err = oras.ExtendedCopyGraph(
		ctx,
		repo,
		nil,
		ocispec.Descriptor{},
		oras.DefaultExtendedCopyGraphOptions,
	)
	if err == nil {
		t.Fatal("ExtendedCopyGraph() error = nil, want non-nil for nil destination")
	}
}
