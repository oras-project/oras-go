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

package config

import (
	"encoding/json"
	"fmt"
	"io"

	"oras.land/oras-go/v2/registry/remote/auth"
)

// ReadOnlyConfig represents authentication credentials parsed from a standard config file,
// which are read to use. It is read-only - only GetCredential is supported.
type ReadOnlyConfig struct {
	auths map[string]auth.Credential
}

// NewReadOnlyConfig creates a new ReadOnlyConfig from the given reader that contains a standard
// config file content. It returns an error if the content is not in the expected format.
func NewReadOnlyConfig(reader io.Reader) (*ReadOnlyConfig, error) {
	var content map[string]json.RawMessage
	if err := json.NewDecoder(reader).Decode(&content); err != nil {
		return nil, err
	}
	var authsCache map[string]json.RawMessage
	if authsBytes, ok := content[configFieldAuths]; ok {
		if err := json.Unmarshal(authsBytes, &authsCache); err != nil {
			return nil, fmt.Errorf("failed to unmarshal auths field: %w: %v", ErrInvalidConfigFormat, err)
		}
	}

	cfg := ReadOnlyConfig{
		auths: make(map[string]auth.Credential, len(authsCache)),
	}
	for serverAddress := range authsCache {
		creds, err := getCredentialFromCache(authsCache, serverAddress)
		if err != nil {
			return nil, err
		}
		cfg.auths[serverAddress] = creds
	}

	return &cfg, nil
}

// GetCredential returns the credential for the given server address. For non-existent server address,
// it returns auth.EmptyCredential.
func (cfg *ReadOnlyConfig) GetCredential(serverAddress string) (auth.Credential, error) {
	if v, ok := cfg.auths[serverAddress]; ok {
		return v, nil
	}
	return auth.EmptyCredential, nil
}
