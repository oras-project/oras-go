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
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// FileStore implements a credentials store using the docker configuration file
// to keep the credentials in plain-text.
//
// Reference: https://docs.docker.com/engine/reference/commandline/cli/#docker-cli-configuration-file-configjson-properties
type FileStore struct {
	// DisablePut disables putting credentials in plaintext.
	// If DisablePut is set to true, Put() will return ErrPlaintextPutDisabled.
	DisablePut bool

	config *Config
}

var (
	// ErrPlaintextPutDisabled is returned by Put() when DisablePut is set
	// to true.
	ErrPlaintextPutDisabled = errors.New("putting plaintext credentials is disabled")
	// ErrBadCredentialFormat is returned by Put() when the credential format
	// is bad.
	ErrBadCredentialFormat = errors.New("bad credential format")
)

// NewFileStore creates a new file credentials store.
//
// Reference: https://docs.docker.com/engine/reference/commandline/cli/#docker-cli-configuration-file-configjson-properties
func NewFileStore(configPath string) (*FileStore, error) {
	cfg, err := Load(configPath)
	if err != nil {
		return nil, err
	}
	return newFileStore(cfg), nil
}

// newFileStore creates a file credentials store based on the given config instance.
func newFileStore(cfg *Config) *FileStore {
	return &FileStore{config: cfg}
}

// Get retrieves credentials from the store for the given server address.
func (fs *FileStore) Get(_ context.Context, serverAddress string) (Credential, error) {
	authCfg, err := fs.config.GetAuthConfig(serverAddress)
	if err != nil {
		return EmptyCredential, err
	}
	return NewCredential(authCfg)
}

// Put saves credentials into the store for the given server address.
// Returns ErrPlaintextPutDisabled if fs.DisablePut is set to true.
func (fs *FileStore) Put(_ context.Context, serverAddress string, cred Credential) error {
	if fs.DisablePut {
		return ErrPlaintextPutDisabled
	}
	if err := validateCredentialFormat(cred); err != nil {
		return err
	}

	authCfg := NewAuthConfig(cred)
	return fs.config.PutAuthConfig(serverAddress, authCfg)
}

// Delete removes credentials from the store for the given server address.
func (fs *FileStore) Delete(_ context.Context, serverAddress string) error {
	return fs.config.DeleteAuthConfig(serverAddress)
}

// validateCredentialFormat validates the format of cred.
func validateCredentialFormat(cred Credential) error {
	if strings.ContainsRune(cred.Username, ':') {
		// Username and password will be encoded in the base64(username:password)
		// format in the file. The decoded result will be wrong if username
		// contains colon(s).
		return fmt.Errorf("%w: colons(:) are not allowed in username", ErrBadCredentialFormat)
	}
	return nil
}

// NewAuthConfig creates an authConfig based on cred.
func NewAuthConfig(cred Credential) AuthConfig {
	return AuthConfig{
		Auth:          encodeAuth(cred.Username, cred.Password),
		IdentityToken: cred.RefreshToken,
		RegistryToken: cred.AccessToken,
	}
}

// encodeAuth base64-encodes username and password into base64(username:password).
func encodeAuth(username, password string) string {
	if username == "" && password == "" {
		return ""
	}
	return base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
}

// NewCredential creates a Credential based on authCfg.
func NewCredential(authCfg AuthConfig) (Credential, error) {
	cred := Credential{
		Username:     authCfg.Username,
		Password:     authCfg.Password,
		RefreshToken: authCfg.IdentityToken,
		AccessToken:  authCfg.RegistryToken,
	}
	if authCfg.Auth != "" {
		var err error
		// override username and password
		cred.Username, cred.Password, err = decodeAuth(authCfg.Auth)
		if err != nil {
			return EmptyCredential, fmt.Errorf("failed to decode auth field: %w: %v", ErrInvalidConfigFormat, err)
		}
	}
	return cred, nil
}

// decodeAuth decodes a base64 encoded string and returns username and password.
func decodeAuth(authStr string) (username string, password string, err error) {
	if authStr == "" {
		return "", "", nil
	}

	decoded, err := base64.StdEncoding.DecodeString(authStr)
	if err != nil {
		return "", "", err
	}
	decodedStr := string(decoded)
	username, password, ok := strings.Cut(decodedStr, ":")
	if !ok {
		return "", "", fmt.Errorf("auth '%s' does not conform the base64(username:password) format", decodedStr)
	}
	return username, password, nil
}
