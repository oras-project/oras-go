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
	"errors"
	"reflect"
	"testing"

	"github.com/oras-project/oras-go/v3/registry"
	"github.com/oras-project/oras-go/v3/registry/remote/properties"
)

type propertiesReferenceParser struct {
	ref properties.Reference
	err error
}

func (p propertiesReferenceParser) ParseReference(string) (properties.Reference, error) {
	return p.ref, p.err
}

type legacyReferenceParser struct {
	ref registry.Reference
	err error
}

func (p legacyReferenceParser) ParseReference(string) (registry.Reference, error) {
	return p.ref, p.err
}

func TestParseReferenceForScope(t *testing.T) {
	legacyRef := registry.Reference{
		Registry:   "registry.example.com",
		Repository: "library/test",
		Reference:  "latest",
	}
	tests := []struct {
		name      string
		parser    any
		want      registry.Reference
		wantMatch bool
		wantErr   error
	}{
		{
			name: "properties reference parser",
			parser: propertiesReferenceParser{ref: properties.Reference{
				Registry:   legacyRef.Registry,
				Repository: legacyRef.Repository,
				Tag:        legacyRef.Reference,
			}},
			want: registry.Reference{
				Registry:   legacyRef.Registry,
				Repository: legacyRef.Repository,
			},
			wantMatch: true,
		},
		{
			name:      "legacy reference parser",
			parser:    legacyReferenceParser{ref: legacyRef},
			want:      legacyRef,
			wantMatch: true,
		},
		{
			name:      "parser error",
			parser:    propertiesReferenceParser{err: errdefTest},
			want:      registry.Reference{},
			wantMatch: true,
			wantErr:   errdefTest,
		},
		{
			name:   "unsupported target",
			parser: struct{}{},
			want:   registry.Reference{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, matched, err := parseReferenceForScope(tt.parser, "latest")
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("parseReferenceForScope() error = %v, want %v", err, tt.wantErr)
			}
			if matched != tt.wantMatch {
				t.Errorf("parseReferenceForScope() matched = %v, want %v", matched, tt.wantMatch)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseReferenceForScope() = %v, want %v", got, tt.want)
			}
		})
	}
}

var errdefTest = errors.New("test error")
