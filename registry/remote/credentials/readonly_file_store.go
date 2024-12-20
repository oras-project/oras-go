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
	"errors"
	"io"

	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/credentials/internal/config"
)

// ReadOnlyFileStore implements a credentials store using the docker configuration file
// as an input. It supports only Get operation that works in the same way as for standard
// FileStore.
type ReadOnlyFileStore struct {
	cfg *config.ReadOnlyConfig
}

// ErrReadOnlyStore is returned for operations
// Put(...) and Delete(...) for read-only store.
var ErrReadOnlyStore = errors.New("cannot modify content of the read-only store")

// NewReadOnlyFileStore creates a new file credentials store based on the given config,
// it returns an error if the config is not in the expected format.
func NewReadOnlyFileStore(reader io.Reader) (*ReadOnlyFileStore, error) {
	cfg, err := config.LoadFromReader(reader)
	if err != nil {
		return nil, err
	}
	return &ReadOnlyFileStore{cfg: cfg}, nil
}

// Get retrieves credentials from the store for the given server address. In case of non-existent
// server address, it returns auth.EmptyCredential.
func (fs *ReadOnlyFileStore) Get(_ context.Context, serverAddress string) (auth.Credential, error) {
	return fs.cfg.GetCredential(serverAddress)
}

// Get always returns ErrReadOnlyStore. It's present to satisfy the Store interface.
func (fs *ReadOnlyFileStore) Put(_ context.Context, _ string, _ auth.Credential) error {
	return ErrReadOnlyStore
}

// Delete always returns ErrReadOnlyStore. It's present to satisfy the Store interface.
func (fs *ReadOnlyFileStore) Delete(_ context.Context, _ string) error {
	return ErrReadOnlyStore
}
