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

package file

import (
	"io"
	"os"
	"path/filepath"
)

// openParentRoot opens the parent directory of dirPath as a root, the way
// Store.pushDir opens its working directory, and returns it with the name of
// dirPath inside it. dirPath itself is not created.
func openParentRoot(dirPath string) (*os.Root, string, error) {
	parent := filepath.Dir(dirPath)
	if err := os.MkdirAll(parent, 0777); err != nil {
		return nil, "", err
	}
	root, err := os.OpenRoot(parent)
	if err != nil {
		return nil, "", err
	}
	return root, filepath.Base(dirPath), nil
}

// extractTarGzip calls extractTarGzipIn with the parent directory of dirPath
// as the root.
func extractTarGzip(dirPath, dirName, gzPath, checksum string, buf []byte, preservePermissions bool) error {
	root, dirRel, err := openParentRoot(dirPath)
	if err != nil {
		return err
	}
	defer root.Close()
	return extractTarGzipIn(root, dirRel, dirPath, dirName, gzPath, checksum, buf, preservePermissions)
}

// extractTarDirectory calls extractTarDirectoryIn with the parent directory of
// dirPath as the root.
func extractTarDirectory(dirPath, dirName string, r io.Reader, buf []byte, preservePermissions bool) error {
	root, dirRel, err := openParentRoot(dirPath)
	if err != nil {
		return err
	}
	defer root.Close()
	return extractTarDirectoryIn(root, dirRel, dirPath, dirName, r, buf, preservePermissions)
}
