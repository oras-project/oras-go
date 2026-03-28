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

//go:build windows

package configpaths

import (
	"os"
	"path/filepath"
)

func newUAPIResolver() *uapiResolver {
	return &uapiResolver{
		// Windows has no vendor (/usr/share) equivalent.
		vendorConfDir: "",
		// %ProgramData% replaces /etc on Windows.
		systemConfDir: windowsProgramData(),
		// %APPDATA% replaces $XDG_CONFIG_HOME on Windows.
		userConfDir:             windowsAppData,
		supportsRootfulRootless: false,
	}
}

// windowsProgramData returns the ProgramData directory, which serves as
// the system-wide config directory on Windows.
func windowsProgramData() string {
	if pd := os.Getenv("ProgramData"); pd != "" {
		return pd
	}
	return ""
}

// windowsAppData returns %APPDATA%/containers as the user config
// directory on Windows.
func windowsAppData() string {
	if appdata := os.Getenv("APPDATA"); appdata != "" {
		return filepath.Join(appdata, containersConfigDir)
	}
	// Fall back to $HOME/.config/containers
	return defaultXDGConfigHome()
}
