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

package oci

import (
	"context"
	_ "crypto/sha256" // SHA-256 path-digest verification
	_ "crypto/sha512" // SHA-384 and SHA-512 path-digest verification
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"sync"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3/content"
	"github.com/oras-project/oras-go/v3/errdef"
	"github.com/oras-project/oras-go/v3/internal/descriptor"
	"github.com/oras-project/oras-go/v3/internal/syncutil"
	"golang.org/x/sync/semaphore"
)

// defaultVerifyConcurrency is the default value of VerifyOptions.Concurrency.
const defaultVerifyConcurrency = 3

// VerifyOptions contains parameters for [Verify].
type VerifyOptions struct {
	// Concurrency limits the maximum number of concurrent blob verifications.
	// If less than or equal to 0, a default (currently 3) is used.
	Concurrency int
}

// VerifyReport summarizes the outcome of [Verify].
//
// Verify runs in "on error resume next" mode: a blob that fails verification
// does not abort the sweep of the rest of the layout, so a report can describe
// a layout that is partially intact.
type VerifyReport struct {
	// Verified lists blob paths whose content hashes to the digest named by
	// the path.
	Verified []VerifyEntry
	// Failed lists blob paths whose content does not match the path digest, or
	// that cannot be read.
	Failed []VerifyEntry
	// Unverifiable lists paths that cannot be verified because the digest
	// algorithm is unsupported, the file name is not a valid encoded digest,
	// or the entry is not a blob file. These are the paths [Store.GC] would
	// skip.
	Unverifiable []VerifyEntry
	// Layout lists problems with the structure of the layout outside of
	// blobs/: a missing or invalid oci-layout or index.json, and the
	// leftovers of interrupted writes (files in ingest/ and temporary
	// index.json or oci-layout files in the layout root). Nothing is deleted,
	// so no leftover expiry applies; each leftover carries its modification
	// time instead.
	//
	// Layout findings do not indicate corrupt content, and a layout that once
	// had a write killed is not corrupt, so they are kept apart from Failed.
	// A layout problem never stops the verification of blobs.
	Layout []LayoutFinding
}

// VerifyEntry is the result of inspecting a single path under blobs/.
type VerifyEntry struct {
	// Path is the path of the entry relative to the root of fsys, using slash
	// separators as in [io/fs].
	Path string
	// Reason is the error that caused the entry to be classified as Failed or
	// Unverifiable. It is nil for Verified entries.
	Reason error
}

type hashItem struct {
	path string
	dgst digest.Digest
}

type verifyAccumulator struct {
	mu           sync.Mutex
	verified     []VerifyEntry
	failed       []VerifyEntry
	unverifiable []VerifyEntry
	layout       []LayoutFinding
}

func (a *verifyAccumulator) addVerified(p string) {
	a.mu.Lock()
	a.verified = append(a.verified, VerifyEntry{Path: p})
	a.mu.Unlock()
}

func (a *verifyAccumulator) addFailed(p string, reason error) {
	a.mu.Lock()
	a.failed = append(a.failed, VerifyEntry{Path: p, Reason: reason})
	a.mu.Unlock()
}

func (a *verifyAccumulator) addUnverifiable(p string, reason error) {
	a.mu.Lock()
	a.unverifiable = append(a.unverifiable, VerifyEntry{Path: p, Reason: reason})
	a.mu.Unlock()
}

func (a *verifyAccumulator) addLayout(f LayoutFinding) {
	a.mu.Lock()
	a.layout = append(a.layout, f)
	a.mu.Unlock()
}

func (a *verifyAccumulator) report() *VerifyReport {
	a.mu.Lock()
	defer a.mu.Unlock()
	return &VerifyReport{
		Verified:     append([]VerifyEntry(nil), a.verified...),
		Failed:       append([]VerifyEntry(nil), a.failed...),
		Unverifiable: append([]VerifyEntry(nil), a.unverifiable...),
		Layout:       append([]LayoutFinding(nil), a.layout...),
	}
}

// Verify walks the blobs directory of an OCI image layout in fsys and checks
// that each blob's content matches the digest named by its path.
//
// It also reports problems with the structure of the layout itself in
// [VerifyReport.Layout].
//
// Verify is read-only: it does not create, modify, or remove any file in fsys.
// It does not compute reachability.
//
// A blob that fails verification, or a problem with the layout files, does not
// stop the sweep. The returned error is reserved for context cancellation and
// for a layout root or blobs directory that cannot be read. On cancellation after the walk has started, the report describes
// the entries processed before the sweep stopped.
func Verify(ctx context.Context, fsys fs.FS, opts VerifyOptions) (*VerifyReport, error) {
	if err := isContextDone(ctx); err != nil {
		return nil, err
	}
	concurrency := opts.Concurrency
	if concurrency <= 0 {
		concurrency = defaultVerifyConcurrency
	}

	acc := &verifyAccumulator{}
	if err := verifyLayout(ctx, fsys, acc); err != nil {
		return nil, err
	}

	algDirs, err := fs.ReadDir(fsys, ocispec.ImageBlobsDir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", ocispec.ImageBlobsDir, err)
	}

	var toHash []hashItem
	for _, algDir := range algDirs {
		if err := isContextDone(ctx); err != nil {
			return acc.report(), err
		}
		algPath := path.Join(ocispec.ImageBlobsDir, algDir.Name())
		if !algDir.IsDir() {
			acc.addUnverifiable(algPath, fmt.Errorf("expected a digest algorithm directory"))
			continue
		}

		alg := digest.Algorithm(algDir.Name())
		supported := descriptor.IsSupportedAlgorithm(alg)
		digestEntries, err := fs.ReadDir(fsys, algPath)
		if err != nil {
			acc.addFailed(algPath, fmt.Errorf("read directory: %w", err))
			continue
		}
		for _, digestEntry := range digestEntries {
			entryPath := path.Join(algPath, digestEntry.Name())
			if !supported {
				acc.addUnverifiable(entryPath, fmt.Errorf("digest algorithm %q: %w", alg, errdef.ErrUnsupported))
				continue
			}
			if !digestEntry.Type().IsRegular() {
				acc.addUnverifiable(entryPath, fmt.Errorf("expected a regular blob file, found %s", digestEntry.Type()))
				continue
			}
			blobDigest := digest.NewDigestFromEncoded(alg, digestEntry.Name())
			if err := blobDigest.Validate(); err != nil {
				acc.addUnverifiable(entryPath, fmt.Errorf("invalid digest %s: %w", blobDigest, err))
				continue
			}
			toHash = append(toHash, hashItem{path: entryPath, dgst: blobDigest})
		}
	}

	if len(toHash) == 0 {
		return acc.report(), nil
	}

	limiter := semaphore.NewWeighted(int64(concurrency))
	err = syncutil.Go(ctx, limiter, func(ctx context.Context, _ *syncutil.LimitedRegion, item hashItem) error {
		if err := verifyBlob(ctx, fsys, item.path, item.dgst); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			acc.addFailed(item.path, err)
			return nil
		}
		acc.addVerified(item.path)
		return nil
	}, toHash...)
	return acc.report(), err
}

func verifyBlob(ctx context.Context, fsys fs.FS, p string, dgst digest.Digest) error {
	f, err := fsys.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()

	verifier := dgst.Verifier()
	buf := make([]byte, 32*1024)
	for {
		if err := isContextDone(ctx); err != nil {
			return err
		}
		n, err := f.Read(buf)
		if n > 0 {
			if _, werr := verifier.Write(buf[:n]); werr != nil {
				return werr
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return err
		}
	}
	if !verifier.Verified() {
		return fmt.Errorf("%w: %s", content.ErrMismatchedDigest, dgst)
	}
	return nil
}
