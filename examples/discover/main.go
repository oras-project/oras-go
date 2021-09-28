package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strings"

	"github.com/containerd/containerd/remotes"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	artifactspec "github.com/oras-project/artifacts-spec/specs-go/v1"
	"oras.land/oras-go/pkg/artifact"
	"oras.land/oras-go/pkg/content"
	"oras.land/oras-go/pkg/oras"
	"oras.land/oras-go/pkg/remotes/docker"
)

const (
	repoRef  = "localhost:5000/net-monitor"
	imageRef = "localhost:5000/net-monitor:v1"
)

var (
	localhostResolver  remotes.Resolver
	example_references []artifactspec.Descriptor
	targetResolver     *content.Memory
)

func main() {
	ctx := context.Background()

	registry, err := content.NewRegistry(content.RegistryOptions{PlainHTTP: true})
	if err != nil {
		panic("could not create a new registry")
	}
	discoverer, err := docker.WithDiscover(imageRef, localhostResolver, &registry.Opts)
	if err != nil {
		panic("could not create discoverer")
	}

	desc, blobs, err := oras.Discover(ctx, discoverer, imageRef, "")
	if err != nil {
		panic("could not discover artifacts" + err.Error())
	}

	output := json.NewEncoder(os.Stdout)
	err = output.Encode(desc)
	if err != nil {
		panic("could not encode artifact manifest to std out")
	}

	for _, b := range blobs {
		err = output.Encode(b)
		if err != nil {
			panic("could not encode artifact blob")
		}
	}

	desc, err = oras.Copy(ctx, discoverer, "localhost:5000/net-monitor:v1", targetResolver, "",
		oras.WithAllowedMediaType(
			artifactspec.MediaTypeArtifactManifest,
			imagespec.MediaTypeImageManifest,
			"application/vnd.docker.image.rootfs.diff.tar.gzip",
			"application/vnd.docker.container.image.v1+json",
			"application/json"),
		oras.WithArtifactFilters(artifact.AnnotationFilter(func(annotations map[string]string) bool {
			val, ok := annotations["test-filter"]
			if !ok {
				return true
			}

			if val == "tested" {
				return false
			}

			return true
		})))
	if err != nil {
		panic("could not copy image: " + err.Error())
	}

	_, _, ok := targetResolver.GetByName("signature.json")
	if !ok {
		panic("expected the signature.json blob to copied over")
	}

	_, _, ok = targetResolver.GetByName("sbom.json")
	if ok {
		panic("did not expect sbom.json to be copied over")
	}
}

func init() {
	memoryStore := content.NewMemory()

	m := &imagespec.Manifest{}

	f, err := os.Open("net-monitor/manifest.json")
	if err != nil {
		panic("could not open net-monitor/manifest.json: " + err.Error())
	}

	manifestBytes, err := ioutil.ReadAll(f)
	if err != nil {
		panic("could not get net-monitor/manifest.json content: " + err.Error())
	}

	err = json.Unmarshal(manifestBytes, m)
	if err != nil {
		panic("could not unmarshal manifest")
	}

	f, err = os.Open("net-monitor/config.json")
	if err != nil {
		panic("could not open net-monitor/config.json: " + err.Error())
	}

	configBytes, err := ioutil.ReadAll(f)
	if err != nil {
		panic("could not get net-monitor/config.json content: " + err.Error())
	}
	memoryStore.Set(m.Config, configBytes)

	f, err = os.Open("net-monitor/layer.tar.gz")
	if err != nil {
		panic("could not open net-monitor/layer.tar.gz " + err.Error())
	}

	layerBytes, err := ioutil.ReadAll(f)
	if err != nil {
		panic("could not get net-monitor/layer.tar.gz content: " + err.Error())
	}
	memoryStore.Set(m.Layers[0], layerBytes)

	imageDesc, err := memoryStore.Add("localhost:5000/net-monitor:v1", imagespec.MediaTypeImageManifest, manifestBytes)
	if err != nil {
		panic("could not add image manifest")
	}
	memoryStore.StoreManifest("localhost:5000/net-monitor:v1", imageDesc, manifestBytes)

	// Create example sbom
	sbom := "sbom.json"
	sbomContent := []byte(`{"version": "0.0.0.0", "artifact": "localhost:5000/net-monitor:v1", "sbom": "good"}`)
	sbomd, err := memoryStore.Add(sbom, "application/json", sbomContent)
	if err != nil {
		panic("could not write to memory store")
	}

	am := content.GenerateArtifactsManifest(
		"sbom/example",
		content.ConvertV1DescriptorToV2(imageDesc, ""),
		make(map[string]string),
		sbomd)

	amc, err := json.Marshal(am)
	if err != nil {
		panic("could not marshal artifact manifest")
	}

	err = memoryStore.StoreManifest("localhost:5000/net-monitor", sbomd, amc)
	if err != nil {
		panic("could not store manifest")
	}

	// Create example signature
	signature := "signature.json"
	signatureContent := []byte(`{"version": "0.0.0.0", "artifact": "localhost:5000/net-monitor:v1", "signature": "signed"}`)
	signatured, err := memoryStore.Add(signature, "application/json", signatureContent)
	if err != nil {
		panic("could not write to memory store")
	}

	am = content.GenerateArtifactsManifest(
		"signature/example",
		content.ConvertV1DescriptorToV2(imageDesc, ""),
		make(map[string]string),
		signatured)

	amc, err = json.Marshal(am)
	if err != nil {
		panic("could not marshal artifact manifest")
	}

	err = memoryStore.StoreManifest("localhost:5000/net-monitor", signatured, amc)
	if err != nil {
		panic("could not store manifest")
	}

	example_references = []artifactspec.Descriptor{
		content.ConvertV1DescriptorToV2(sbomd, "sbom/example"),
		content.ConvertV1DescriptorToV2(signatured, "signature/example"),
	}

	// Create mock server handlers
	http.Handle("localhost/v2/net-monitor/blobs",
		&mockRegistry{
			ref:         "localhost:5000/net-monitor:v1",
			memorystore: memoryStore})
	http.Handle("localhost/v2/net-monitor/manifests/sha256:d9cbd5e9195d3d68fd126ec8fcd687042d1eb767c57de4d776830c3545b534d4",
		&mockRegistry{
			ref:         "localhost:5000/net-monitor:v1",
			memorystore: memoryStore})
	http.Handle("localhost/v2/net-monitor/manifests/v1",
		&mockRegistry{
			ref:         "localhost:5000/net-monitor:v1",
			memorystore: memoryStore})
	http.Handle("localhost/oras/artifacts/v1/net-monitor/manifests/",
		&mockRegistry{
			ref:         "localhost:5000/net-monitor:v1",
			memorystore: memoryStore,
			references:  example_references})

	go http.ListenAndServe(":5000", http.DefaultServeMux)

	localhostResolver = memoryStore
}

func init() {
	target := content.NewMemory()

	targetMux := &http.ServeMux{}
	targetMux.Handle("localhost/v2/net-monitor/manifests/v1",
		&mockRegistry{
			ref:         "localhost:5002/net-monitor:v1",
			memorystore: target})
	targetMux.Handle("localhost/oras/artifacts/v1/net-monitor/manifests",
		&mockRegistry{
			ref:         "localhost:5002/net-monitor:v1",
			memorystore: target,
			references:  example_references,
		})

	go http.ListenAndServe(":5002", targetMux)

	targetResolver = target
}

type mockRegistry struct {
	ref         string
	memorystore *content.Memory
	references  []artifactspec.Descriptor
}

func (e *mockRegistry) ServeHTTP(writer http.ResponseWriter, req *http.Request) {
	if strings.Contains(req.URL.String(), "blobs") {
		return
	}

	_, desc, err := e.memorystore.Resolve(req.Context(), e.ref)
	if err != nil {
		writer.WriteHeader(http.StatusNotFound)
		return
	}

	if req.Method == http.MethodHead {
		headers := writer.Header()
		headers.Set("Docker-Content-Digest", desc.Digest.String())
		headers.Set("Content-Type", desc.MediaType)
		headers.Set("Content-Length", fmt.Sprint(desc.Size))
		writer.WriteHeader(http.StatusOK)
		return
	}

	if req.Method != http.MethodGet {
		writer.WriteHeader(http.StatusBadRequest)
		return
	}

	if strings.Contains(req.URL.Path, "referrers") && strings.Contains(req.URL.Path, "sha256:d9cbd5e9195d3d68fd126ec8fcd687042d1eb767c57de4d776830c3545b534d4") {
		exampleArtifacts := struct {
			References []artifactspec.Descriptor `json:"references"`
		}{
			References: e.references,
		}

		exampleArtifacts.References[0].Annotations["test-filter"] = "tested"

		c, err := json.Marshal(exampleArtifacts)
		if err != nil {
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}

		written, err := writer.Write(c)
		if err != nil {
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}

		writer.Header().Set("Docker-Content-Digest", string(desc.Digest))
		writer.Header().Set("Content-Length", fmt.Sprint(written))
		writer.Header().Set("Content-Type", "application/json")
		return
	}

	desc, content, ok := e.memorystore.Get(desc)
	if !ok {
		writer.WriteHeader(http.StatusNoContent)
		return
	}

	written, err := writer.Write(content)
	if err != nil {
		writer.WriteHeader(http.StatusInternalServerError)
		return
	}

	if written <= 0 {
		writer.WriteHeader(http.StatusAccepted)
		return
	}

	writer.Header().Set("Docker-Content-Digest", string(desc.Digest))
	writer.Header().Set("Content-Type", desc.MediaType)
	writer.Header().Set("Content-Length", fmt.Sprint(desc.Size))
}
