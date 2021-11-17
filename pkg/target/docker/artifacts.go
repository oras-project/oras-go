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

package docker

import (
	"context"
	"io"
	"io/ioutil"
	"net/http"
	"time"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/remotes/docker"
	remoteserrors "github.com/containerd/containerd/remotes/errors"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

// CheckManifest is a function that checks if a manifests exists by descriptor
func (d *dockerDiscoverer) CheckManifest(ctx context.Context, host docker.RegistryHost, desc ocispec.Descriptor) error {
	url, err := d.FormatManifestAPI(host, desc.Digest.String())
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", desc.MediaType)

	resp, err := d.client.Do(ctx, req)
	if err != nil {
		if err.Error() == "HTTP 404" {
			return nil
		}
		return err
	}

	if resp.StatusCode <= 299 {
		return nil
	}

	return remoteserrors.NewUnexpectedStatusErr(resp)
}

// PreparePutManifest is a function that prepares to put a manifest
func (d *dockerDiscoverer) PreparePutManifest(ctx context.Context, host docker.RegistryHost, desc ocispec.Descriptor) (content.Writer, error) {
	d.tracker.SetStatus(d.reference, docker.Status{
		Status: content.Status{
			Ref:       d.reference,
			Total:     desc.Size,
			Expected:  desc.Digest,
			StartedAt: time.Now(),
		},
	})

	url, err := d.FormatManifestAPI(host, desc.Digest.String())
	if err != nil {
		return nil, err
	}

	pr, pw := io.Pipe()
	respC := make(chan *http.Response, 1)

	body := ioutil.NopCloser(pr)

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, body)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", desc.MediaType)

	go func() {
		defer close(respC)
		resp, err := d.client.Do(ctx, req)
		if err != nil {
			respC <- &http.Response{
				Status:     err.Error(),
				StatusCode: http.StatusNotModified,
				Request:    req,
			}
			pr.CloseWithError(err)
			return
		}

		switch resp.StatusCode {
		case http.StatusOK, http.StatusCreated, http.StatusNoContent:
		default:
			err := remoteserrors.NewUnexpectedStatusErr(resp)
			pr.CloseWithError(err)
		}
		respC <- resp
	}()

	return &artifactsManifest{
		ref:       d.reference,
		expected:  desc.Digest,
		pipe:      pw,
		responseC: respC,
		tracker:   d.tracker,
	}, nil
}

// artifactsManifest is an internal type that implements the content.Writer interface
type artifactsManifest struct {
	ref       string
	expected  digest.Digest
	pipe      *io.PipeWriter
	tracker   docker.StatusTracker
	responseC <-chan *http.Response
}

func (pw *artifactsManifest) Write(p []byte) (n int, err error) {
	status, err := pw.tracker.GetStatus(pw.ref)
	if err != nil {
		return n, err
	}
	n, err = pw.pipe.Write(p)
	status.Offset += int64(n)
	status.UpdatedAt = time.Now()
	pw.tracker.SetStatus(pw.ref, status)
	return
}

func (pw *artifactsManifest) Close() error {
	return pw.pipe.Close()
}

func (pw *artifactsManifest) Status() (content.Status, error) {
	status, err := pw.tracker.GetStatus(pw.ref)
	if err != nil {
		return content.Status{}, err
	}
	return status.Status, nil

}

func (pw *artifactsManifest) Digest() digest.Digest {
	return pw.expected
}

func (pw *artifactsManifest) Commit(ctx context.Context, size int64, expected digest.Digest, opts ...content.Opt) error {
	// Check whether read has already thrown an error
	if _, err := pw.pipe.Write([]byte{}); err != nil && err != io.ErrClosedPipe {
		return errors.Wrap(err, "pipe error before commit")
	}

	if err := pw.pipe.Close(); err != nil {
		return err
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case resp := <-pw.responseC:
		if resp != nil && resp.StatusCode > 299 {
			return remoteserrors.NewUnexpectedStatusErr(resp)
		}
		defer resp.Body.Close()

		// 201 is specified return status, some registries return
		// 200, 202 or 204.
		switch resp.StatusCode {
		case http.StatusOK, http.StatusCreated, http.StatusNoContent, http.StatusAccepted:
		default:
			return remoteserrors.NewUnexpectedStatusErr(resp)
		}

		status, err := pw.tracker.GetStatus(pw.ref)
		if err != nil {
			return errors.Wrap(err, "failed to get status")
		}

		if size > 0 && size != status.Offset {
			return errors.Errorf("unexpected size %d, expected %d", status.Offset, size)
		}

		if expected == "" {
			expected = status.Expected
		}

		actual, err := digest.Parse(resp.Header.Get("Docker-Content-Digest"))
		if err != nil {
			return errors.Wrap(err, "invalid content digest in response")
		}

		if actual != expected {
			return errors.Errorf("got digest %s, expected %s", actual, expected)
		}

		status.Committed = true
		status.UpdatedAt = time.Now()
		pw.tracker.SetStatus(pw.ref, status)
	}

	return nil
}

func (pw *artifactsManifest) Truncate(size int64) error {
	return errors.New("cannot truncate remote upload")
}
