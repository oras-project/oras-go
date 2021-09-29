package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strings"
	"time"

	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	artifactspec "github.com/oras-project/artifacts-spec/specs-go/v1"
	"oras.land/oras-go/pkg/content"
)

func CreateExampleTestServerIfLocalServerIsNotRunning(e *exampleState) {
	c := &http.Client{Timeout: time.Second * 10}

	req, err := http.NewRequest(http.MethodHead, "localhost:5000/v2/", nil)
	if err != nil {
		panic(err.Error())
	}

	_, err = c.Do(req)
	if err != nil {
		initExampleState(e)
		initTargetExampleState(e)
		return
	}

	reg, err := content.NewRegistry(content.RegistryOptions{PlainHTTP: true})
	if err != nil {
		panic(err.Error())
	}

	e.localhostResolver = reg
	e.targetResolver = content.NewMemory()
}

func initExampleState(e *exampleState) {
	memoryStore := content.NewMemory()

	m := &imagespec.Manifest{}

	f, err := os.Open("net-monitor/manifest.json")
	fail(err, "could not open net-monitor/manifest.json")

	manifestBytes, err := ioutil.ReadAll(f)
	fail(err, "could not get net-monitor/manifest.json content")

	err = json.Unmarshal(manifestBytes, m)
	fail(err, "could not unmarshal manifest")

	f, err = os.Open("net-monitor/config.json")
	fail(err, "could not open net-monitor/config.json")

	configBytes, err := ioutil.ReadAll(f)
	fail(err, "could not get net-monitor/config.json content")

	memoryStore.Set(m.Config, configBytes)
	f, err = os.Open("net-monitor/layer.tar.gz")
	fail(err, "could not open net-monitor/layer.tar.gz")

	layerBytes, err := ioutil.ReadAll(f)
	fail(err, "could not get net-monitor/layer.tar.gz content")

	memoryStore.Set(m.Layers[0], layerBytes)

	imageDesc, err := memoryStore.Add("localhost:5000/net-monitor:v1", imagespec.MediaTypeImageManifest, manifestBytes)
	fail(err, "could not add image manifest")

	memoryStore.StoreManifest("localhost:5000/net-monitor:v1", imageDesc, manifestBytes)
	// Create example sbom
	sbom := "sbom.json"
	sbomContent := []byte(`{"version": "0.0.0.0", "artifact": "localhost:5000/net-monitor:v1", "sbom": "good"}`)
	sbomd, err := memoryStore.Add(sbom, "application/json", sbomContent)
	fail(err, "could not write to memory store")

	am := content.GenerateArtifactsManifest(
		"sbom/example",
		content.ConvertV1DescriptorToV2(imageDesc, ""),
		make(map[string]string),
		sbomd)

	amc, err := json.Marshal(am)
	fail(err, "could not marshal artifact manifest")

	err = memoryStore.StoreManifest("localhost:5000/net-monitor", sbomd, amc)
	fail(err, "could not store manifest")

	// Create example signature
	signature := "signature.json"
	signatureContent := []byte(`{"version": "0.0.0.0", "artifact": "localhost:5000/net-monitor:v1", "signature": "signed"}`)
	signatured, err := memoryStore.Add(signature, "application/json", signatureContent)
	fail(err, "could not write to memory store")

	am = content.GenerateArtifactsManifest(
		"signature/example",
		content.ConvertV1DescriptorToV2(imageDesc, ""),
		make(map[string]string),
		signatured)

	amc, err = json.Marshal(am)
	fail(err, "could not marshal artifact manifest")

	err = memoryStore.StoreManifest("localhost:5000/net-monitor", signatured, amc)
	fail(err, "could not store manifest")

	e.example_references = []artifactspec.Descriptor{
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
			references:  e.example_references})

	go http.ListenAndServe(":5000", http.DefaultServeMux)

	e.localhostResolver = memoryStore
}

func initTargetExampleState(e *exampleState) {
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
			references:  e.example_references,
		})

	go http.ListenAndServe(":5002", targetMux)

	e.targetResolver = target
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
