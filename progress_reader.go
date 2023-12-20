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
	"io"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

type CopyUpdate struct {
	Copied     int64
	Descriptor ocispec.Descriptor
}

type progressReader struct {
	desc ocispec.Descriptor
	r    io.ReadCloser
	c    chan<- CopyUpdate
}

func (p *progressReader) Close() error {
	return p.r.Close()
}

func (p *progressReader) Read(buf []byte) (int, error) {
	n, err := p.r.Read(buf)
	if n > 0 {
		p.c <- CopyUpdate{Copied: int64(n), Descriptor: p.desc}
	}
	return n, err
}
