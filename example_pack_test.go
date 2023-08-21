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

package oras_test

import (
	"bytes"
	"context"
	"fmt"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	oras "oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/memory"
)

func ExamplePackManifest_imageV1() {
	store := memory.New()

	data := [][]byte{
		[]byte(`hello`),
		[]byte(`world`),
	}
	layers := make([]ocispec.Descriptor, 0, len(data))
	ctx := context.Background()
	for _, data := range data {
		desc := content.NewDescriptorFromBytes("test/layer", data)
		layers = append(layers, desc)
		if err := store.Push(ctx, desc, bytes.NewReader(data)); err != nil {
			panic(err)
		}
	}

	opts := oras.PackManifestOptions{
		ManifestAnnotations: map[string]string{
			ocispec.AnnotationCreated: "2000-01-01T00:00:00Z", // for testing purpose
		},
	}
	manifestDesc, err := oras.PackManifest(ctx, store, oras.PackManifestTypeImageV1_0, "test/artifact", opts)
	if err != nil {
		panic(err)
	}
	fmt.Println(manifestDesc)

	// verify manifest
	manifestData, err := content.FetchAll(ctx, store, manifestDesc)
	if err != nil {
		panic(err)
	}
	fmt.Println(string(manifestData))

	// Output:
	// {application/vnd.oci.image.manifest.v1+json sha256:b3b0939bcc66de501485cf003c2c2aca7b6c38e58b6b0d458548d7ef6bd4decc 293 [] map[org.opencontainers.image.created:2000-01-01T00:00:00Z] [] <nil> test/artifact}
	// {"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"test/artifact","digest":"sha256:44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a","size":2},"layers":[],"annotations":{"org.opencontainers.image.created":"2000-01-01T00:00:00Z"}}
}
