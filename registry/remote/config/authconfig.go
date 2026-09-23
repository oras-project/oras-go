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
	"github.com/oras-project/oras-go/v3/internal/authtype"
)

// AuthConfig contains authorization information for connecting to a Registry.
// References:
//   - https://github.com/docker/cli/blob/v24.0.0-beta.2/cli/config/configfile/file.go#L17-L45
//   - https://github.com/docker/cli/blob/v24.0.0-beta.2/cli/config/types/authconfig.go#L3-L22
type AuthConfig = authtype.AuthConfig

// ErrInvalidAuthConfig is returned when the auth config format is invalid.
var ErrInvalidAuthConfig = authtype.ErrInvalidAuthConfig

// NewAuthConfig creates an AuthConfig based on credential components.
var NewAuthConfig = authtype.NewAuthConfig

// EncodeAuth base64-encodes username and password into base64(username:password).
var EncodeAuth = authtype.EncodeAuth
