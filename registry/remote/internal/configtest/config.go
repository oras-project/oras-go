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

package configtest

// Config represents the structure of a config file for testing purpose.
type Config struct {
	AuthConfigs       map[string]AuthConfig `json:"auths"`
	CredentialsStore  string                `json:"credsStore,omitempty"`
	CredentialHelpers map[string]string     `json:"credHelpers,omitempty"`
	SomeConfigField   int                   `json:"some_config_field"`
}

// AuthConfig represents the structure of the "auths" field of a config file
// for testing purpose.
type AuthConfig struct {
	SomeAuthField string `json:"some_auth_field,omitempty"`
	Username      string `json:"username,omitempty"`
	Password      string `json:"password,omitempty"`
	Auth          string `json:"auth,omitempty"`

	// IdentityToken is used to authenticate the user and get
	// an access token for the registry.
	IdentityToken string `json:"identitytoken,omitempty"`
	// RegistryToken is a bearer token to be sent to a registry
	RegistryToken string `json:"registrytoken,omitempty"`
}
