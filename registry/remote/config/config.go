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
	"github.com/oras-project/oras-go/v3/registry/remote/internal/configfile"
)

// The Docker configuration file format lives in an internal package so that
// this package and registry/remote/credentials can both use it without either
// importing the other. This package imports credentials to build credential
// stores, so credentials cannot import it back. The names are re-exported here
// unchanged; configfile is an implementation detail.

// Config represents a Docker configuration file.
type Config = configfile.Config

// ErrInvalidConfigFormat is returned when the config format is invalid.
var ErrInvalidConfigFormat = configfile.ErrInvalidConfigFormat

// ErrNoConfigPath is returned when no config path is configured.
var ErrNoConfigPath = configfile.ErrNoConfigPath

// NewConfig creates a new Config with no backing file.
// Use this when you want to configure credentials programmatically
// without persisting them to disk.
var NewConfig = configfile.NewConfig

// NewConfigWithPath creates a new Config associated with the given path.
var NewConfigWithPath = configfile.NewConfigWithPath

// Load loads a Config from the given path.
var Load = configfile.Load

// ToHostname normalizes a registry address into a hostname.
var ToHostname = configfile.ToHostname
