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

package credentials

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/credentials/internal/config"
)

// MemoryStore is a store that keeps credentials in memory.
type MemoryStore struct {
	store sync.Map
}

// NewMemoryStore creates a new in-memory credentials store.
func NewMemoryStore() Store {
	return &MemoryStore{}
}

// NewMemoryStoreFromConfig creates a new in-memory credentials store from the given configuration.
//
// Reference: https://docs.docker.com/engine/reference/commandline/cli/#docker-cli-configuration-file-configjson-properties
func NewMemoryStoreFromConfig(c []byte) (*MemoryStore, error) {
	cfg := struct {
		Auths map[string]config.AuthConfig `json:"auths"`
	}{}
	if err := json.Unmarshal(c, &cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal auth field: %w: %v", config.ErrInvalidConfigFormat, err)
	}

	s := &MemoryStore{}
	for addr, auth := range cfg.Auths {
		// Normalize the auth key to hostname.
		hostname := config.ToHostname(addr)
		cred, err := auth.Credential()
		if err != nil {
			return nil, err
		}
		s.store.Store(hostname, cred)
	}
	return s, nil
}

// Get retrieves credentials from the store for the given server address.
func (ms *MemoryStore) Get(_ context.Context, serverAddress string) (auth.Credential, error) {
	cred, found := ms.store.Load(serverAddress)
	if !found {
		return auth.EmptyCredential, nil
	}
	return cred.(auth.Credential), nil
}

// Put saves credentials into the store for the given server address.
func (ms *MemoryStore) Put(_ context.Context, serverAddress string, cred auth.Credential) error {
	ms.store.Store(serverAddress, cred)
	return nil
}

// Delete removes credentials from the store for the given server address.
func (ms *MemoryStore) Delete(_ context.Context, serverAddress string) error {
	ms.store.Delete(serverAddress)
	return nil
}
