//go:build !windows

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
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func Test_extractTarDirectory_PreservePermissions(t *testing.T) {
	fileContent := "hello world"
	fileMode := os.FileMode(0771)
	tarData := createTar(t, []tarEntry{
		{name: "base/", mode: os.ModeDir | 0777},
		{name: "base/test.txt", content: fileContent, mode: fileMode},
	})

	tempDir := t.TempDir()
	dirName := "base"
	dirPath := filepath.Join(tempDir, dirName)
	buf := make([]byte, 1024)

	if err := extractTarDirectory(dirPath, dirName, bytes.NewReader(tarData), buf, true); err != nil {
		t.Fatalf("extractTarDirectory() error = %v", err)
	}

	filePath := filepath.Join(dirPath, "test.txt")
	fi, err := os.Lstat(filePath)
	if err != nil {
		t.Fatalf("failed to stat file %s: %v", filePath, err)
	}

	gotContent, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("failed to read file %s: %v", filePath, err)
	}
	if string(gotContent) != fileContent {
		t.Errorf("file content = %s, want %s", gotContent, fileContent)
	}

	if fi.Mode() != fileMode {
		t.Errorf("file %q mode = %s, want %s", fi.Name(), fi.Mode(), fileMode)
	}
}
