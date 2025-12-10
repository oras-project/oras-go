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

package registry

import (
	"fmt"
	"reflect"
	"testing"
)

func TestParseReferenceList(t *testing.T) {
	const (
		digest1 = "sha256:1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"
		digest2 = "sha256:abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"
		digest3 = "sha256:fedcba0987654321fedcba0987654321fedcba0987654321fedcba0987654321"
	)

	tests := []struct {
		name    string
		input   string
		want    []Reference
		wantErr bool
	}{
		{
			name:  "tag list with localhost and port",
			input: "localhost:5000/hello:v1,v2,v3",
			want: []Reference{
				{
					Registry:   "localhost:5000",
					Repository: "hello",
					Reference:  "v1",
				},
				{
					Registry:   "localhost:5000",
					Repository: "hello",
					Reference:  "v2",
				},
				{
					Registry:   "localhost:5000",
					Repository: "hello",
					Reference:  "v3",
				},
			},
			wantErr: false,
		},
		{
			name:  "tag list with registry domain",
			input: "registry.example.com/myapp:latest,stable,1.0.0",
			want: []Reference{
				{
					Registry:   "registry.example.com",
					Repository: "myapp",
					Reference:  "latest",
				},
				{
					Registry:   "registry.example.com",
					Repository: "myapp",
					Reference:  "stable",
				},
				{
					Registry:   "registry.example.com",
					Repository: "myapp",
					Reference:  "1.0.0",
				},
			},
			wantErr: false,
		},
		{
			name:  "digest list",
			input: fmt.Sprintf("localhost:5000/hello@%s,%s,%s", digest1, digest2, digest3),
			want: []Reference{
				{
					Registry:   "localhost:5000",
					Repository: "hello",
					Reference:  digest1,
				},
				{
					Registry:   "localhost:5000",
					Repository: "hello",
					Reference:  digest2,
				},
				{
					Registry:   "localhost:5000",
					Repository: "hello",
					Reference:  digest3,
				},
			},
			wantErr: false,
		},
		{
			name:  "single tag",
			input: "localhost:5000/hello:v1",
			want: []Reference{
				{
					Registry:   "localhost:5000",
					Repository: "hello",
					Reference:  "v1",
				},
			},
			wantErr: false,
		},
		{
			name:  "single digest",
			input: fmt.Sprintf("localhost:5000/hello@%s", digest1),
			want: []Reference{
				{
					Registry:   "localhost:5000",
					Repository: "hello",
					Reference:  digest1,
				},
			},
			wantErr: false,
		},
		{
			name:  "tag list with spaces",
			input: "localhost:5000/hello:v1, v2, v3",
			want: []Reference{
				{
					Registry:   "localhost:5000",
					Repository: "hello",
					Reference:  "v1",
				},
				{
					Registry:   "localhost:5000",
					Repository: "hello",
					Reference:  "v2",
				},
				{
					Registry:   "localhost:5000",
					Repository: "hello",
					Reference:  "v3",
				},
			},
			wantErr: false,
		},
		{
			name:  "nested repository with tags",
			input: "registry.example.com/org/team/project:dev,staging,prod",
			want: []Reference{
				{
					Registry:   "registry.example.com",
					Repository: "org/team/project",
					Reference:  "dev",
				},
				{
					Registry:   "registry.example.com",
					Repository: "org/team/project",
					Reference:  "staging",
				},
				{
					Registry:   "registry.example.com",
					Repository: "org/team/project",
					Reference:  "prod",
				},
			},
			wantErr: false,
		},
		{
			name:    "empty string",
			input:   "",
			want:    nil,
			wantErr: true,
		},
		{
			name:    "missing delimiter",
			input:   "localhost:5000/hello",
			want:    nil,
			wantErr: true,
		},
		{
			name:    "empty tag in list",
			input:   "localhost:5000/hello:v1,,v3",
			want:    nil,
			wantErr: true,
		},
		{
			name:    "invalid tag",
			input:   "localhost:5000/hello:v1,INVALID!,v3",
			want:    nil,
			wantErr: true,
		},
		{
			name:    "invalid digest",
			input:   "localhost:5000/hello@sha256:invalid,sha256:digest2",
			want:    nil,
			wantErr: true,
		},
		{
			name:    "missing repository",
			input:   "localhost:5000:v1,v2",
			want:    nil,
			wantErr: true,
		},
		{
			name:    "only comma",
			input:   "localhost:5000/hello:,",
			want:    nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseReferenceList(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseReferenceList() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				return
			}
			if len(got) != len(tt.want) {
				t.Errorf("ParseReferenceList() returned %d references, want %d", len(got), len(tt.want))
				return
			}
			for i, ref := range got {
				if !reflect.DeepEqual(ref, tt.want[i]) {
					t.Errorf("ParseReferenceList() reference[%d] = %v, want %v", i, ref, tt.want[i])
				}
			}
		})
	}
}
