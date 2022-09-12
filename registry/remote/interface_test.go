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

package remote_test

import (
	"testing"

	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/internal/interfaces"
	"oras.land/oras-go/v2/registry"
	"oras.land/oras-go/v2/registry/remote"
)

func TestRepositoryInterface(t *testing.T) {
	var repo interface{} = &remote.Repository{}
	if _, ok := repo.(registry.Repository); !ok {
		t.Error("&Repository{} does not conform registry.Repository")
	}
	if _, ok := repo.(oras.GraphTarget); !ok {
		t.Error("&Repository{} does not conform oras.GraphTarget")
	}
	if _, ok := repo.(interfaces.ReferenceParser); !ok {
		t.Error("&Repository{} does not conform interfaces.ReferenceParser")
	}
}
