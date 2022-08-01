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

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/registry"
)

// Tag tags the descriptor identified by src with dst.
func Tag(ctx context.Context, target Target, src, dst string) error {
	if refTagger, ok := target.(registry.ReferenceTagger); ok {
		return refTagger.TagReference(ctx, src, dst)
	}
	refFetcher, okFetch := target.(registry.ReferenceFetcher)
	refPusher, okPush := target.(registry.ReferencePusher)
	if okFetch && okPush {
		desc, rc, err := refFetcher.FetchReference(ctx, src)
		if err != nil {
			return err
		}
		defer rc.Close()
		return refPusher.PushReference(ctx, desc, rc, dst)
	}
	desc, err := target.Resolve(ctx, src)
	if err != nil {
		return err
	}
	return target.Tag(ctx, desc, dst)
}

var (
	// DefaultResolveOptions provides the default ResolveOptions.
	DefaultResolveOptions = ResolveOptions{
		TargetPlatform: nil,
	}
)

// ResolveOptions contains parameters for oras.Resolve.
type ResolveOptions struct {
	// TargetPlatform is the target platform.
	// Will do the platform selection if specified.
	TargetPlatform *ocispec.Platform
}

// Resolve resolves a descriptor with provided reference from the target.
func Resolve(ctx context.Context, target Target, ref string, opts ResolveOptions) (ocispec.Descriptor, error) {
	desc, err := target.Resolve(ctx, ref)
	if err != nil {
		return ocispec.Descriptor{}, err
	}

	if opts.TargetPlatform != nil {
		desc, err = selectPlatform(ctx, target, desc, opts.TargetPlatform)
		if err != nil {
			return ocispec.Descriptor{}, err
		}
	}

	return desc, nil
}
