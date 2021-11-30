package httputil

import (
	"errors"
	"fmt"
	"io"
	"net/http"
)

// readSeekCloser seeks http body by starting new connections.
type readSeekCloser struct {
	client *http.Client
	req    *http.Request
	rc     io.ReadCloser
	size   int64
	offset int64
}

// NewReadSeekCloser returns a seeker to make the HTTP response seekable.
// Callers should ensure that the server supports Range request.
func NewReadSeekCloser(client *http.Client, req *http.Request, respBody io.ReadCloser, size int64) io.ReadSeekCloser {
	return &readSeekCloser{
		client: client,
		req:    req,
		rc:     respBody,
		size:   size,
	}
}

// Read reads the content body and counts offset.
func (rsc *readSeekCloser) Read(p []byte) (n int, err error) {
	n, err = rsc.rc.Read(p)
	rsc.offset += int64(n)
	return
}

// Seek starts a new connection to the remote for reading if position changes.
func (rsc *readSeekCloser) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekCurrent:
		offset += rsc.offset
	case io.SeekStart:
		// no-op
	case io.SeekEnd:
		offset += rsc.size
	default:
		return 0, errors.New("invalid whence")
	}
	if offset < 0 || offset > rsc.size {
		return 0, fmt.Errorf("invalid offset: %d / %d", offset, rsc.size)
	}
	if offset == rsc.offset {
		return offset, nil
	}
	if offset == rsc.size {
		rsc.rc.Close()
		rsc.rc = http.NoBody
		rsc.offset = offset
		return offset, nil
	}

	req := rsc.req.Clone(rsc.req.Context())
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", offset, rsc.size-1))
	resp, err := rsc.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("seek: %w", err)
	}
	if resp.StatusCode != http.StatusPartialContent {
		resp.Body.Close()
		return 0, fmt.Errorf("seek: %s %q: unexpected status code %d", resp.Request.Method, resp.Request.URL, resp.StatusCode)
	}

	rsc.rc.Close()
	rsc.rc = resp.Body
	rsc.offset = offset
	return offset, nil
}

// Close closes the content body.
func (rsc *readSeekCloser) Close() error {
	return rsc.rc.Close()
}
