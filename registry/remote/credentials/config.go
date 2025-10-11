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
	"errors"
)

const (
	// configFieldAuths is the "auths" field in the config file.
	// Reference: https://github.com/docker/cli/blob/v24.0.0-beta.2/cli/config/configfile/file.go#L19
	configFieldAuths = "auths"
	// configFieldCredentialsStore is the "credsStore" field in the config file.
	configFieldCredentialsStore = "credsStore"
	// configFieldCredentialHelpers is the "credHelpers" field in the config file.
	configFieldCredentialHelpers = "credHelpers"
)

// ErrInvalidConfigFormat is returned when the config format is invalid.
var ErrInvalidConfigFormat = errors.New("invalid config format")

// Config represents a configuration interface for credential management.
type Config interface {
	// GetCredential returns a Credential for the given server address.
	GetCredential(serverAddress string) (Credential, error)

	// PutCredential stores a credential for the given server address.
	PutCredential(serverAddress string, cred Credential) error

	// DeleteCredential removes the credential for the given server address.
	DeleteCredential(serverAddress string) error

	// GetCredentialHelper returns the credential helper for the given server address.
	GetCredentialHelper(serverAddress string) string

	// CredentialsStore returns the configured credentials store.
	CredentialsStore() string

	// SetCredentialsStore sets the credentials store.
	SetCredentialsStore(credsStore string) error

	// IsAuthConfigured returns whether authentication is configured.
	IsAuthConfigured() bool

	// Path returns the path to the configuration file.
	Path() string
}

