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
	"encoding/json"
	"errors"
	"fmt"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	artifactspec "github.com/oras-project/artifacts-spec/specs-go/v1"
	"oras.land/oras-go/pkg/target"
)

// GraphWalkFunc is a type of function that is applied to each object fetched
type GraphWalkFunc func(artifactspec.Descriptor, artifactspec.Manifest, []target.Object) error

// Graph is a function that searches for objects that share the subject reference as a root
// if artifact type is specified only objects of that artifact type will be discovered
// Graph works recursively, and will traverse each branch in order to collevt all the objects that should
// be included in the graph
// As the graph is being walked, the walkfn will be called on each artifactspec manifest encountered;
// which is a chance to download content discovered, vice versa
// The entry point for the search is a source Target, the Target is expected to be able to discover, fetch, and resolve
func Graph(ctx context.Context, subject, artifactType string, source target.Target, walkfn GraphWalkFunc) ([]target.Object, error) {
	desc, discovered, err := Discover(ctx, source, subject, artifactType)
	if err != nil {
		return nil, err
	}

	fetcher, err := source.Fetcher(ctx, subject)
	if err != nil {
		return nil, err
	}

	var output []target.Object

	// the first element will always be the root
	output = append(output, target.FromOCIDescriptor(subject, desc, "", nil))

	for _, m := range discovered {
		var (
			artifactManifest   artifactspec.Manifest
			artifactDescriptor artifactspec.Descriptor
		)
		reader, err := fetcher.Fetch(ctx, ocispec.Descriptor{
			Digest:      m.Digest,
			Size:        m.Size,
			Annotations: m.Annotations,
			MediaType:   m.MediaType,
		})
		if err != nil {
			return nil, err
		}
		defer reader.Close()

		err = json.NewDecoder(reader).Decode(&artifactManifest)
		if err != nil {
			return nil, err
		}

		artifactDescriptor = artifactspec.Descriptor{
			Digest:       m.Digest,
			Size:         m.Size,
			Annotations:  m.Annotations,
			MediaType:    m.MediaType,
			ArtifactType: artifactManifest.ArtifactType,
		}
		objects, err := target.FromArtifactManifest(subject, artifactDescriptor, artifactManifest)
		if err != nil {
			return nil, err
		}

		if walkfn != nil {
			err := walkfn(artifactDescriptor, artifactManifest, objects)
			if err != nil {
				// If the walk function returns back ErrSkipObjects, further processing stops
				if errors.Is(err, ErrSkipObjects) {
					continue
				}
				return nil, err
			}
		}

		// Skip the first element because it is the root of the tree
		output = append(output, objects[1:]...)

		// Get the locator from the root so we can call Graph on the current child
		_, host, namespace, _, err := objects[0].ReferenceSpec()
		if err != nil {
			return nil, err
		}

		additional, err := Graph(ctx, fmt.Sprintf("%s/%s@%s", host, namespace, m.Digest), artifactType, source, walkfn)
		if err != nil {
			return nil, err
		}

		if len(additional) > 1 && err == nil {
			output = append(output, additional[1:]...)
		}
	}

	return output, nil
}
