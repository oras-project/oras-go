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

// docker-credential-oras-test is a minimal Docker credential helper for use
// in functional tests. It stores credentials in a JSON file whose path is
// read from the ORAS_TEST_CRED_STORE environment variable.
//
// It implements the docker-credential-helper protocol:
//
//	echo "registry.example.com" | docker-credential-oras-test get
//	echo '{"ServerURL":…,"Username":…,"Secret":…}' | docker-credential-oras-test store
//	echo "registry.example.com" | docker-credential-oras-test erase
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

type credential struct {
	Username string `json:"Username"`
	Secret   string `json:"Secret"`
}

type credStore map[string]credential

func loadStore(path string) credStore {
	s := make(credStore)
	data, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	_ = json.Unmarshal(data, &s)
	return s
}

func saveStore(path string, s credStore) error {
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: docker-credential-oras-test <get|store|erase>")
		os.Exit(1)
	}

	storePath := os.Getenv("ORAS_TEST_CRED_STORE")
	if storePath == "" {
		fmt.Fprintln(os.Stderr, "ORAS_TEST_CRED_STORE environment variable not set")
		os.Exit(1)
	}

	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading stdin: %v\n", err)
		os.Exit(1)
	}

	action := os.Args[1]
	s := loadStore(storePath)

	switch action {
	case "get":
		server := strings.TrimSpace(string(input))
		cred, ok := s[server]
		if !ok {
			fmt.Fprintln(os.Stderr, "credentials not found in native keychain")
			os.Exit(1)
		}
		if err := json.NewEncoder(os.Stdout).Encode(map[string]string{
			"ServerURL": server,
			"Username":  cred.Username,
			"Secret":    cred.Secret,
		}); err != nil {
			fmt.Fprintf(os.Stderr, "encoding credentials: %v\n", err)
			os.Exit(1)
		}

	case "store":
		var dc struct {
			ServerURL string `json:"ServerURL"`
			Username  string `json:"Username"`
			Secret    string `json:"Secret"`
		}
		if err := json.Unmarshal(input, &dc); err != nil {
			fmt.Fprintf(os.Stderr, "decoding input: %v\n", err)
			os.Exit(1)
		}
		s[dc.ServerURL] = credential{Username: dc.Username, Secret: dc.Secret}
		if err := saveStore(storePath, s); err != nil {
			fmt.Fprintf(os.Stderr, "saving store: %v\n", err)
			os.Exit(1)
		}

	case "erase":
		server := strings.TrimSpace(string(input))
		delete(s, server)
		if err := saveStore(storePath, s); err != nil {
			fmt.Fprintf(os.Stderr, "saving store: %v\n", err)
			os.Exit(1)
		}

	default:
		fmt.Fprintf(os.Stderr, "unknown action: %s\n", action)
		os.Exit(1)
	}
}
