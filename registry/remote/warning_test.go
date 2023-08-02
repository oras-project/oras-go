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

package remote

import (
	"errors"
	"reflect"
	"testing"
)

func Test_parseWarningHeader(t *testing.T) {
	tests := []struct {
		name    string
		header  string
		want    WarningValue
		wantErr error
	}{
		{
			name:   "Valid warning",
			header: `299 - "This is a warning."`,
			want: WarningValue{
				Code:  299,
				Agent: "-",
				Text:  "This is a warning.",
			},
		},
		{
			name:   "Valid meaningless warning",
			header: `299 - " "`,
			want: WarningValue{
				Code:  299,
				Agent: "-",
				Text:  " ",
			},
		},
		{
			name:    "Multiple spaces in warning",
			header:  `299  -   "This is a warning."`,
			want:    WarningValue{},
			wantErr: errUnexpectedWarningFormat,
		},
		{
			name:    "Leading space in warning",
			header:  ` 299 - "This is a warning."`,
			want:    WarningValue{},
			wantErr: errUnexpectedWarningFormat,
		},
		{
			name:    "Trailing space in warning",
			header:  `299 - "This is a warning." `,
			want:    WarningValue{},
			wantErr: errUnexpectedWarningFormat,
		},
		{
			name:    "Warning with a non-299 code",
			header:  `199 - "This is a warning."`,
			want:    WarningValue{},
			wantErr: errUnexpectedWarningFormat,
		},
		{
			name:    "Warning with a non-unknown agent",
			header:  `299 localhost:5000 "This is a warning."`,
			want:    WarningValue{},
			wantErr: errUnexpectedWarningFormat,
		},
		{
			name:    "Warning with a date",
			header:  `299 - "This is a warning." "Sat, 25 Aug 2012 23:34:45 GMT"`,
			want:    WarningValue{},
			wantErr: errUnexpectedWarningFormat,
		},
		{
			name:    "Invalid format",
			header:  `299 - "This is a warning." something strange`,
			want:    WarningValue{},
			wantErr: errUnexpectedWarningFormat,
		},
		{
			name:    "Not a warning",
			header:  `foo bar baz`,
			want:    WarningValue{},
			wantErr: errUnexpectedWarningFormat,
		},
		{
			name:    "No code",
			header:  `- "This is a warning."`,
			want:    WarningValue{},
			wantErr: errUnexpectedWarningFormat,
		},
		{
			name:    "No agent",
			header:  `299 "This is a warning."`,
			want:    WarningValue{},
			wantErr: errUnexpectedWarningFormat,
		},
		{
			name:    "No text",
			header:  `299 -`,
			want:    WarningValue{},
			wantErr: errUnexpectedWarningFormat,
		},
		{
			name:    "Empty text",
			header:  `299 - ""`,
			want:    WarningValue{},
			wantErr: errUnexpectedWarningFormat,
		},
		{
			name:    "Unquoted text",
			header:  `299 - This is a warning.`,
			want:    WarningValue{},
			wantErr: errUnexpectedWarningFormat,
		},
		{
			name:    "Single-quoted text",
			header:  `299 - 'This is a warning.'`,
			want:    WarningValue{},
			wantErr: errUnexpectedWarningFormat,
		},
		{
			name:    "Back-quoted text",
			header:  "299 - `This is a warning.`",
			want:    WarningValue{},
			wantErr: errUnexpectedWarningFormat,
		},
		{
			name:    "Invalid quotes",
			header:  `299 - 'This is a warning."`,
			want:    WarningValue{},
			wantErr: errUnexpectedWarningFormat,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseWarningHeader(tt.header)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("parseWarningHeader() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseWarningHeader() = %v, want %v", got, tt.want)
			}
		})
	}
}
