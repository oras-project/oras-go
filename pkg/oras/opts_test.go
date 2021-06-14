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

package oras

import (
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/suite"
)

type PushOptsSuite struct {
	suite.Suite
}

func (suite *PushOptsSuite) TestValidateNameAsPath() {
	var err error

	// valid path
	err = ValidateNameAsPath(descFromName("hello.txt"))
	suite.NoError(err, "valid path")
	err = ValidateNameAsPath(descFromName("foo/bar"))
	suite.NoError(err, "valid path with multiple sub-directories")

	// no empty name
	err = ValidateNameAsPath(descFromName(""))
	suite.Error(err, "empty path")

	// path should be clean
	err = ValidateNameAsPath(descFromName("./hello.txt"))
	suite.Error(err, "dirty path")
	err = ValidateNameAsPath(descFromName("foo/../bar"))
	suite.Error(err, "dirty path")

	// path should be slash-separated
	err = ValidateNameAsPath(descFromName("foo\\bar"))
	suite.Error(err, "path not slash separated")

	// disallow absolute path
	err = ValidateNameAsPath(descFromName("/foo/bar"))
	suite.Error(err, "unix: absolute path disallowed")
	err = ValidateNameAsPath(descFromName("C:\\foo\\bar"))
	suite.Error(err, "windows: absolute path disallowed")
	err = ValidateNameAsPath(descFromName("C:/foo/bar"))
	suite.Error(err, "windows: absolute path disallowed")

	// disallow path traversal
	err = ValidateNameAsPath(descFromName(".."))
	suite.Error(err, "path traversal disallowed")
	err = ValidateNameAsPath(descFromName("../bar"))
	suite.Error(err, "path traversal disallowed")
	err = ValidateNameAsPath(descFromName("foo/../../bar"))
	suite.Error(err, "path traversal disallowed")
}

func TestPushOptsSuite(t *testing.T) {
	suite.Run(t, new(PushOptsSuite))
}

func descFromName(name string) ocispec.Descriptor {
	return ocispec.Descriptor{
		Annotations: map[string]string{
			ocispec.AnnotationTitle: name,
		},
	}
}
