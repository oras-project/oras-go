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

package config_test

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/oras-project/oras-go/v3/registry/remote/config"
)

func ExampleRegistriesConfig_RegistryProperties() {
	// Create a sample registries.conf file.
	tmpDir, err := os.MkdirTemp("", "registries-example")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tmpDir)

	confContent := `
[[registry]]
prefix = "docker.io"
location = "registry-1.docker.io"

[[registry]]
prefix = "insecure.example.com"
insecure = true
`
	confPath := filepath.Join(tmpDir, "registries.conf")
	if err := os.WriteFile(confPath, []byte(confContent), 0644); err != nil {
		panic(err)
	}

	// Load registries configuration.
	regConf, err := config.LoadRegistriesConfig(confPath)
	if err != nil {
		panic(err)
	}

	// Convert config to properties for a docker.io reference.
	props, err := regConf.RegistryProperties("docker.io/library/alpine:latest")
	if err != nil {
		panic(err)
	}

	fmt.Println("Registry:", props.Reference.Registry)
	fmt.Println("Repository:", props.Reference.Repository)
	fmt.Println("Tag:", props.Reference.Tag)
	fmt.Println("Insecure:", props.Transport.Insecure)
	// Output:
	// Registry: registry-1.docker.io
	// Repository: library/alpine
	// Tag: latest
	// Insecure: false
}

func ExampleNewRegistryProperties() {
	// Create properties without a registries config (programmatic flow).
	props, err := config.NewRegistryProperties("ghcr.io/user/repo:v1", nil)
	if err != nil {
		panic(err)
	}

	fmt.Println("Registry:", props.Reference.Registry)
	fmt.Println("Repository:", props.Reference.Repository)
	fmt.Println("Tag:", props.Reference.Tag)
	// Output:
	// Registry: ghcr.io
	// Repository: user/repo
	// Tag: v1
}

func ExampleRegistriesConfig_RegistryProperties_mirrors() {
	// Create a sample registries.conf file with mirrors.
	tmpDir, err := os.MkdirTemp("", "registries-mirror-example")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tmpDir)

	confContent := `
[[registry]]
prefix = "docker.io"
location = "registry-1.docker.io"

[[registry.mirror]]
location = "mirror1.example.com"
insecure = true

[[registry.mirror]]
location = "mirror2.example.com"
pull-from-mirror = "digest-only"
`
	confPath := filepath.Join(tmpDir, "registries.conf")
	if err := os.WriteFile(confPath, []byte(confContent), 0644); err != nil {
		panic(err)
	}

	// Load registries configuration.
	regConf, err := config.LoadRegistriesConfig(confPath)
	if err != nil {
		panic(err)
	}

	// Convert config to properties for a docker.io reference.
	props, err := regConf.RegistryProperties("docker.io/library/alpine:latest")
	if err != nil {
		panic(err)
	}

	fmt.Println("Registry:", props.Reference.Registry)
	fmt.Printf("Mirrors: %d\n", len(props.Mirrors))
	for i, m := range props.Mirrors {
		fmt.Printf("  Mirror %d: %s (insecure=%v, pull-from-mirror=%s)\n",
			i, m.Location, m.Transport.Insecure, m.PullFromMirror)
	}
	// Output:
	// Registry: registry-1.docker.io
	// Mirrors: 2
	//   Mirror 0: mirror1.example.com (insecure=true, pull-from-mirror=)
	//   Mirror 1: mirror2.example.com (insecure=false, pull-from-mirror=digest-only)
}

func ExampleRegistriesConfig_SearchRegistryProperties() {
	// Create a sample registries.conf file.
	tmpDir, err := os.MkdirTemp("", "registries-search-example")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tmpDir)

	confContent := `
unqualified-search-registries = ["docker.io", "quay.io"]

[[registry]]
prefix = "docker.io"
location = "registry-1.docker.io"
`
	confPath := filepath.Join(tmpDir, "registries.conf")
	if err := os.WriteFile(confPath, []byte(confContent), 0644); err != nil {
		panic(err)
	}

	// Load registries configuration.
	regConf, err := config.LoadRegistriesConfig(confPath)
	if err != nil {
		panic(err)
	}

	// Search for an unqualified image name across configured search registries.
	results, err := regConf.SearchRegistryProperties("library/alpine:latest")
	if err != nil {
		panic(err)
	}

	for _, props := range results {
		fmt.Printf("%s/%s:%s\n", props.Reference.Registry, props.Reference.Repository, props.Reference.Tag)
	}
	// Output:
	// registry-1.docker.io/library/alpine:latest
	// quay.io/library/alpine:latest
}

func ExampleLoadCertsDirFromPaths() {
	// Set up a temporary containers-certs.d directory structure.
	tmpDir, err := os.MkdirTemp("", "certsd-example")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a per-host directory with certificate files.
	hostDir := filepath.Join(tmpDir, "myregistry.example.com")
	if err := os.MkdirAll(hostDir, 0755); err != nil {
		panic(err)
	}
	if err := os.WriteFile(filepath.Join(hostDir, "ca.crt"), []byte("ca-data"), 0644); err != nil {
		panic(err)
	}
	if err := os.WriteFile(filepath.Join(hostDir, "client.cert"), []byte("cert-data"), 0644); err != nil {
		panic(err)
	}
	if err := os.WriteFile(filepath.Join(hostDir, "client.key"), []byte("key-data"), 0600); err != nil {
		panic(err)
	}

	// Discover certificate files for the registry host.
	cd, err := config.LoadCertsDirFromPaths("myregistry.example.com", []string{tmpDir})
	if err != nil {
		panic(err)
	}

	fmt.Printf("CA certs: %d\n", len(cd.CACertPaths))
	fmt.Printf("Client cert found: %v\n", cd.ClientCert != "")
	fmt.Printf("Client key found: %v\n", cd.ClientKey != "")
	// Output:
	// CA certs: 1
	// Client cert found: true
	// Client key found: true
}
