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

package credentials

import (
	"errors"
	"testing"
)

func Test_validateCredentialFormat(t *testing.T) {
	tests := []struct {
		name    string
		cred    Credential
		wantErr error
	}{
		{
			name: "Username contains colon",
			cred: Credential{
				Username: "x:y",
				Password: "z",
			},
			wantErr: ErrBadCredentialFormat,
		},
		{
			name: "Password contains colon",
			cred: Credential{
				Username: "x",
				Password: "y:z",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateCredentialFormat(tt.cred); !errors.Is(err, tt.wantErr) {
				t.Errorf("validateCredentialFormat() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
