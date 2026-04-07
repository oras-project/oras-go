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
		vendorConfDir:           "",
		systemConfDir:           windowsProgramData(),
		userConfDir:             windowsAppData,
		supportsRootfulRootless: false,
	}
}

func windowsProgramData() string {
	if pd := os.Getenv("ProgramData"); pd != "" {
		return pd
	}
	return ""
}

func windowsAppData() string {
	if appdata := os.Getenv("APPDATA"); appdata != "" {
		return filepath.Join(appdata, containersConfigDir)
	}
	return defaultXDGConfigHome()
}
