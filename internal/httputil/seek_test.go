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

package httputil

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
)

func Test_readSeekCloser_Read(t *testing.T) {
	content := []byte("hello world")
	path := "/testpath"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if _, err := w.Write(content); err != nil {
			t.Errorf("failed to write %q: %v", r.URL, err)
		}
	}))
	defer ts.Close()

	client := ts.Client()
	resp, err := client.Get(ts.URL + path)
	if err != nil {
		t.Fatalf("failed to do request: %v", err)
	}
	rsc := NewReadSeekCloser(client, resp.Request, resp.Body, int64(len(content)))
	buf := bytes.NewBuffer(nil)
	if _, err := buf.ReadFrom(rsc); err != nil {
		t.Errorf("fail to read: %v", err)
	}
	if got := buf.Bytes(); !bytes.Equal(got, content) {
		t.Errorf("readSeekCloser.Read() = %v, want %v", got, content)
	}
	if err := rsc.Close(); err != nil {
		t.Errorf("fail to close: %v", err)
	}
	if !rsc.(*readSeekCloser).closed {
		t.Errorf("readSeekCloser not closed")
	}
}

func Test_readSeekCloser_Seek(t *testing.T) {
	content := []byte("hello world")
	path := "/testpath"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		rangeHeader := r.Header.Get("Range")
		if rangeHeader == "" {
			w.WriteHeader(http.StatusOK)
			if _, err := w.Write(content); err != nil {
				t.Errorf("failed to write %q: %v", r.URL, err)
			}
			return
		}
		var start, end int
		_, err := fmt.Sscanf(rangeHeader, "bytes=%d-%d", &start, &end)
		if err != nil {
			t.Errorf("invalid range header: %s", rangeHeader)
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		if start < 0 || start > end || start >= len(content) {
			t.Errorf("invalid range: %s", rangeHeader)
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		end++
		if end > len(content) {
			end = len(content)
		}
		w.WriteHeader(http.StatusPartialContent)
		if _, err := w.Write(content[start:end]); err != nil {
			t.Errorf("failed to write %q: %v", r.URL, err)
		}
	}))
	defer ts.Close()

	client := ts.Client()
	resp, err := client.Get(ts.URL + path)
	if err != nil {
		t.Fatalf("failed to do request: %v", err)
	}
	rsc := NewReadSeekCloser(client, resp.Request, resp.Body, int64(len(content)))

	tests := []struct {
		name       string
		offset     int64
		whence     int
		wantOffset int64
		n          int64
		want       []byte
		skipSeek   bool
	}{
		{
			name:     "read from initial response",
			n:        3,
			want:     []byte("hel"),
			skipSeek: true,
		},
		{
			name:       "seek to skip",
			offset:     2,
			whence:     io.SeekCurrent,
			wantOffset: 5,
			n:          4,
			want:       []byte(" wor"),
		},
		{
			name:       "seek to the beginning",
			offset:     0,
			whence:     io.SeekStart,
			wantOffset: 0,
			n:          5,
			want:       []byte("hello"),
		},
		{
			name:       "seek to middle",
			offset:     6,
			whence:     io.SeekStart,
			wantOffset: 6,
			n:          math.MaxInt64,
			want:       []byte("world"),
		},
		{
			name:       "seek from end",
			offset:     -4,
			whence:     io.SeekEnd,
			wantOffset: 7,
			n:          3,
			want:       []byte("orl"),
		},
		{
			name:       "seek to the end",
			offset:     0,
			whence:     io.SeekEnd,
			wantOffset: 11,
			n:          5,
			want:       nil,
		},
		{
			name:       "seek beyond the end",
			offset:     42,
			whence:     io.SeekStart,
			wantOffset: 42,
			n:          10,
			want:       nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.skipSeek {
				got, err := rsc.Seek(tt.offset, tt.whence)
				if err != nil {
					t.Errorf("readSeekCloser.Seek() error = %v", err)
				}
				if got != tt.wantOffset {
					t.Errorf("readSeekCloser.Read() = %v, want %v", got, tt.wantOffset)
				}
			}
			buf := bytes.NewBuffer(nil)
			if _, err := buf.ReadFrom(io.LimitReader(rsc, tt.n)); err != nil {
				t.Errorf("fail to read: %v", err)
			}
			if got := buf.Bytes(); !bytes.Equal(got, tt.want) {
				t.Errorf("readSeekCloser.Read() = %v, want %v", got, tt.want)
			}
		})
	}

	_, err = rsc.Seek(-1, io.SeekStart)
	if err == nil {
		t.Errorf("readSeekCloser.Seek() error = %v, wantErr %v", err, true)
	}

	if err := rsc.Close(); err != nil {
		t.Errorf("fail to close: %v", err)
	}
	if !rsc.(*readSeekCloser).closed {
		t.Errorf("readSeekCloser not closed")
	}

	_, err = rsc.Seek(0, io.SeekStart)
	if err == nil {
		t.Errorf("readSeekCloser.Seek() error = %v, wantErr %v", err, true)
	}
}
