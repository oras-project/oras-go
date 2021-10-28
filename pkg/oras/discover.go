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
package oras

import (
	"context"
	"errors"

	"github.com/containerd/containerd/remotes"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	artifactspec "github.com/oras-project/artifacts-spec/specs-go/v1"
)

// Discover is a function that discovers artifacts that reference a subject reference
func Discover(ctx context.Context, resolver remotes.Resolver, subjectReference, artifactType string) (ocispec.Descriptor, []artifactspec.Descriptor, error) {
	discoverer, ok := resolver.(interface {
		Discover(ctx context.Context, subject ocispec.Descriptor, artifactType string) ([]artifactspec.Descriptor, error)
	})

	if !ok {
		return ocispec.Descriptor{}, nil, errors.New("resolver does not implement discover extension method")
	}

	_, desc, err := resolver.Resolve(ctx, subjectReference)
	if err != nil {
		return ocispec.Descriptor{}, nil, err
	}

	artifacts, err := discoverer.Discover(ctx, desc, artifactType)
	if err != nil {
		return ocispec.Descriptor{}, nil, err
	}

	return desc, artifacts, err
}
