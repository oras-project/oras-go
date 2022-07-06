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
	"io"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/registry"
)

// Tag tags a referenced descriptor with a new reference string.
func Tag(ctx context.Context, target Target, srcRef, dstRef string) error {
	var manifestDesc ocispec.Descriptor
	var err error
	if refFetcher, ok := target.(registry.ReferenceFetcher); ok {
		var rc io.ReadCloser
		manifestDesc, rc, err = refFetcher.FetchReference(ctx, srcRef)
		if err != nil {
			return err
		}
		defer rc.Close()
		if refPusher, ok := target.(registry.ReferencePusher); ok {
			return refPusher.PushReference(ctx, manifestDesc, rc, dstRef)
		}
	} else {
		// If target does not implement ReferenceFetcher, need to use
		// Resolve to get the descriptor first, then tag it with Tag.
		manifestDesc, err = target.Resolve(ctx, srcRef)
		if err != nil {
			return err
		}
	}
	return target.Tag(ctx, manifestDesc, dstRef)
}
