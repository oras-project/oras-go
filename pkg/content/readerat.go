package content

import (
	"io"

	"github.com/containerd/containerd/content"
)

// ensure interface
var (
	_ content.ReaderAt = sizeReaderAt{}
)

type readAtCloser interface {
	io.ReaderAt
	io.Closer
}

type sizeReaderAt struct {
	readAtCloser
	size int64
}

func (ra sizeReaderAt) Size() int64 {
	return ra.size
}

func NopCloserAt(r io.ReaderAt) nopCloserAt {
	return nopCloserAt{r}
}

type nopCloserAt struct {
	io.ReaderAt
}

func (n nopCloserAt) Close() error {
	return nil
}

// readerAtWrapper wraps a ReaderAt to give a Reader
type ReaderAtWrapper struct {
	offset   int64
	readerAt io.ReaderAt
}

func (r *ReaderAtWrapper) Read(p []byte) (n int, err error) {
	n, err = r.readerAt.ReadAt(p, r.offset)
	r.offset += int64(n)
	return
}

func NewReaderAtWrapper(readerAt io.ReaderAt) *ReaderAtWrapper {
	return &ReaderAtWrapper{readerAt: readerAt}
}
