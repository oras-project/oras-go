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

package models

import (
	"errors"
	"fmt"

	"github.com/opencontainers/go-digest"
)

var (
	// ErrNoFetcher indicates that no fetcher was provided for content retrieval.
	ErrNoFetcher = errors.New("no fetcher provided")

	// ErrNoPusher indicates that no pusher was provided for content storage.
	ErrNoPusher = errors.New("no pusher provided")

	// ErrNoContent indicates that no content is available.
	ErrNoContent = errors.New("no content available")

	// ErrInvalidManifest indicates that the manifest is invalid or malformed.
	ErrInvalidManifest = errors.New("invalid manifest")

	// ErrNoClient indicates that no client was provided.
	ErrNoClient = errors.New("no client provided")

	// ErrNotLoaded indicates that the manifest has not been loaded yet.
	// Call Load(ctx) or any method that accepts a context to load the manifest
	// before serializing.
	ErrNotLoaded = errors.New("manifest not loaded: call Load(ctx) first")

	// ErrNoDeleter indicates that the target does not support deletion.
	ErrNoDeleter = errors.New("target does not support deletion")
)

// OrmError provides structured error context for ORM operations.
type OrmError struct {
	Op     string        // Operation that failed (e.g., "load", "fetch_blobs").
	Digest digest.Digest // Digest of the content involved, if known.
	Err    error         // Underlying error.
}

func (e *OrmError) Error() string {
	if e.Digest == "" {
		return fmt.Sprintf("orm %s: %s", e.Op, e.Err)
	}
	return fmt.Sprintf("orm %s %s: %s", e.Op, e.Digest, e.Err)
}

func (e *OrmError) Unwrap() error {
	return e.Err
}
