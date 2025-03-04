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
	"context"
	"fmt"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/memory"
)

// ExampleImageV11 demonstrates packing an OCI Image Manifest as defined in
// image-spec v1.1.1.
func ExamplePackManifest_imageV11() {
	// 0. Create a storage
	store := memory.New()

	// 1. Set optional parameters
	opts := oras.PackManifestOptions{
		ManifestAnnotations: map[string]string{
			// this time stamp will be automatically generated if not specified
			// use a fixed value here to make the pack result reproducible
			ocispec.AnnotationCreated: "2000-01-01T00:00:00Z",
		},
	}
	ctx := context.Background()

	// 2. Pack a manifest
	artifactType := "application/vnd.example+type"
	manifestDesc, err := oras.PackManifest(ctx, store, oras.PackManifestVersion1_1, artifactType, opts)
	if err != nil {
		panic(err)
	}
	fmt.Println("Manifest descriptor:", manifestDesc)

	// 3. Verify the packed manifest
	manifestData, err := content.FetchAll(ctx, store, manifestDesc)
	if err != nil {
		panic(err)
	}
	fmt.Println("Manifest content:", string(manifestData))

	// Output:
	// Manifest descriptor: {application/vnd.oci.image.manifest.v1+json sha256:c259a195a48d8029d75449579c81269ca6225cd5b57d36073a7de6458afdfdbd 528 [] map[org.opencontainers.image.created:2000-01-01T00:00:00Z] [] <nil> application/vnd.example+type}
	// Manifest content: {"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","artifactType":"application/vnd.example+type","config":{"mediaType":"application/vnd.oci.empty.v1+json","digest":"sha256:44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a","size":2,"data":"e30="},"layers":[{"mediaType":"application/vnd.oci.empty.v1+json","digest":"sha256:44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a","size":2,"data":"e30="}],"annotations":{"org.opencontainers.image.created":"2000-01-01T00:00:00Z"}}
}

// ExampleImageV10 demonstrates packing an OCI Image Manifest as defined in
// image-spec v1.0.2.
func ExamplePackManifest_imageV10() {
	// 0. Create a storage
	store := memory.New()

	// 1. Set optional parameters
	opts := oras.PackManifestOptions{
		ManifestAnnotations: map[string]string{
			// this time stamp will be automatically generated if not specified
			// use a fixed value here to make the pack result reproducible
			ocispec.AnnotationCreated: "2000-01-01T00:00:00Z",
		},
	}
	ctx := context.Background()

	// 2. Pack a manifest
	artifactType := "application/vnd.example+type"
	manifestDesc, err := oras.PackManifest(ctx, store, oras.PackManifestVersion1_0, artifactType, opts)
	if err != nil {
		panic(err)
	}
	fmt.Println("Manifest descriptor:", manifestDesc)

	// 3. Verify the packed manifest
	manifestData, err := content.FetchAll(ctx, store, manifestDesc)
	if err != nil {
		panic(err)
	}
	fmt.Println("Manifest content:", string(manifestData))

	// Output:
	// Manifest descriptor: {application/vnd.oci.image.manifest.v1+json sha256:da221a11559704e4971c3dcf6564303707a333c8de8cb5475fc48b0072b36c19 308 [] map[org.opencontainers.image.created:2000-01-01T00:00:00Z] [] <nil> application/vnd.example+type}
	// Manifest content: {"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.example+type","digest":"sha256:44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a","size":2},"layers":[],"annotations":{"org.opencontainers.image.created":"2000-01-01T00:00:00Z"}}
}
