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

package models_test

import (
	"errors"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/oras-project/oras-go/v3/objects/models"
)

func TestObjectsError_Error_WithDigest(t *testing.T) {
	dgst := digest.FromString("test content")
	underlying := errors.New("connection refused")
	err := &models.ObjectsError{Op: "load", Digest: dgst, Err: underlying}

	got := err.Error()
	want := "objects load " + string(dgst) + ": connection refused"
	if got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestObjectsError_Error_WithoutDigest(t *testing.T) {
	underlying := errors.New("not found")
	err := &models.ObjectsError{Op: "fetch", Err: underlying}

	got := err.Error()
	want := "objects fetch: not found"
	if got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestObjectsError_Unwrap(t *testing.T) {
	sentinel := errors.New("sentinel error")
	err := &models.ObjectsError{Op: "delete", Err: sentinel}

	if !errors.Is(err, sentinel) {
		t.Error("errors.Is() should find the underlying error via Unwrap()")
	}
}
