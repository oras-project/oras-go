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

package remote

import (
	"context"
	"io"
	"strings"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// PullFromMirror constants control when a mirror should be used.
const (
	// PullFromMirrorAll allows all references (tags and digests).
	PullFromMirrorAll = "all"

	// PullFromMirrorDigestOnly allows only digest references.
	PullFromMirrorDigestOnly = "digest-only"

	// PullFromMirrorTagOnly allows only tag references.
	PullFromMirrorTagOnly = "tag-only"
)

// mirrorRepository pairs a Repository with its pull policy.
type mirrorRepository struct {
	*Repository
	pullFromMirror string
}

// shouldUseForReference reports whether this mirror should handle the given
// reference string based on the PullFromMirror policy.
func (m *mirrorRepository) shouldUseForReference(reference string) bool {
	isDigest := isDigestReference(reference)
	switch m.pullFromMirror {
	case PullFromMirrorDigestOnly:
		return isDigest
	case PullFromMirrorTagOnly:
		return !isDigest
	default:
		// "all" or empty string
		return true
	}
}

// isDigestReference reports whether a reference string is a digest reference.
// A reference is considered a digest if it contains "@" or starts with a
// digest algorithm prefix (e.g., "sha256:").
func isDigestReference(reference string) bool {
	if strings.Contains(reference, "@") {
		return true
	}
	// Bare digest references like "sha256:abc..."
	if strings.Contains(reference, ":") {
		// Could be a host:port or algorithm:hex pattern. Digest algorithms
		// are lowercase ASCII with no dots, while host portions may contain
		// dots or uppercase letters.
		parts := strings.SplitN(reference, ":", 2)
		algo := parts[0]
		// Valid OCI digest algorithms: sha256, sha384, sha512, etc.
		if len(algo) > 0 && algo == strings.ToLower(algo) && !strings.Contains(algo, ".") && !strings.Contains(algo, "/") {
			return true
		}
	}
	return false
}

// isMirrorFallbackError reports whether the error should trigger a fallback
// to the next mirror or the primary registry. Context cancellation and
// deadline exceeded errors are not retryable.
func isMirrorFallbackError(err error) bool {
	if err == nil {
		return false
	}
	return err != context.Canceled && err != context.DeadlineExceeded
}

// withMirrorFallbackResolve tries to resolve the reference against each
// applicable mirror in order, falling back to the primary repository on error.
func withMirrorFallbackResolve(
	ctx context.Context,
	mirrors []mirrorRepository,
	primary *Repository,
	reference string,
	resolve func(ctx context.Context, repo *Repository, reference string) (ocispec.Descriptor, error),
) (ocispec.Descriptor, error) {
	for i := range mirrors {
		if !mirrors[i].shouldUseForReference(reference) {
			continue
		}
		desc, err := resolve(ctx, mirrors[i].Repository, reference)
		if err == nil {
			return desc, nil
		}
		if !isMirrorFallbackError(err) {
			return ocispec.Descriptor{}, err
		}
	}
	return resolve(ctx, primary, reference)
}

// withMirrorFallbackFetch tries to fetch the descriptor from each applicable
// mirror in order, falling back to the primary repository on error.
func withMirrorFallbackFetch(
	ctx context.Context,
	mirrors []mirrorRepository,
	primary *Repository,
	target ocispec.Descriptor,
	fetch func(ctx context.Context, repo *Repository, target ocispec.Descriptor) (io.ReadCloser, error),
) (io.ReadCloser, error) {
	// Fetch by descriptor is always a digest-based operation.
	for i := range mirrors {
		if !mirrors[i].shouldUseForReference(target.Digest.String()) {
			continue
		}
		rc, err := fetch(ctx, mirrors[i].Repository, target)
		if err == nil {
			return rc, nil
		}
		if !isMirrorFallbackError(err) {
			return nil, err
		}
	}
	return fetch(ctx, primary, target)
}

// withMirrorFallbackFetchReference tries to fetch by reference from each
// applicable mirror in order, falling back to the primary repository on error.
func withMirrorFallbackFetchReference(
	ctx context.Context,
	mirrors []mirrorRepository,
	primary *Repository,
	reference string,
	fetch func(ctx context.Context, repo *Repository, reference string) (ocispec.Descriptor, io.ReadCloser, error),
) (ocispec.Descriptor, io.ReadCloser, error) {
	for i := range mirrors {
		if !mirrors[i].shouldUseForReference(reference) {
			continue
		}
		desc, rc, err := fetch(ctx, mirrors[i].Repository, reference)
		if err == nil {
			return desc, rc, nil
		}
		if !isMirrorFallbackError(err) {
			return ocispec.Descriptor{}, nil, err
		}
	}
	return fetch(ctx, primary, reference)
}

// withMirrorFallbackExists tries to check existence against each applicable
// mirror in order, falling back to the primary repository on error.
func withMirrorFallbackExists(
	ctx context.Context,
	mirrors []mirrorRepository,
	primary *Repository,
	target ocispec.Descriptor,
	exists func(ctx context.Context, repo *Repository, target ocispec.Descriptor) (bool, error),
) (bool, error) {
	// Exists by descriptor is always a digest-based operation.
	for i := range mirrors {
		if !mirrors[i].shouldUseForReference(target.Digest.String()) {
			continue
		}
		ok, err := exists(ctx, mirrors[i].Repository, target)
		if err == nil {
			return ok, nil
		}
		if !isMirrorFallbackError(err) {
			return false, err
		}
	}
	return exists(ctx, primary, target)
}
