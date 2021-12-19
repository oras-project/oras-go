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
	"net/http"
	"net/url"
	"testing"
)

func Test_parseLink(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		header  string
		want    string
		wantErr bool
	}{
		{
			name:   "catalog",
			url:    "https://localhost:5000/v2/_catalog",
			header: `</v2/_catalog?last=alpine&n=1>; rel="next"`,
			want:   "https://localhost:5000/v2/_catalog?last=alpine&n=1",
		},
		{
			name:   "list tag",
			url:    "https://localhost:5000/v2/hello-world/tags/list",
			header: `</v2/hello-world/tags/list?last=latest&n=1>; rel="next"`,
			want:   "https://localhost:5000/v2/hello-world/tags/list?last=latest&n=1",
		},
		{
			name:   "other domain",
			url:    "https://localhost:5000/v2/_catalog",
			header: `<https://localhost:5001/v2/_catalog?last=alpine&n=1>; rel="next"`,
			want:   "https://localhost:5001/v2/_catalog?last=alpine&n=1",
		},
		{
			name:    "invalid header",
			url:     "https://localhost:5000/v2/_catalog",
			header:  `</v2/_catalog`,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url, err := url.Parse(tt.url)
			if err != nil {
				t.Errorf("fail to parse url in the test case: %v", err)
			}
			resp := &http.Response{
				Request: &http.Request{
					URL: url,
				},
				Header: http.Header{
					"Link": []string{tt.header},
				},
			}
			got, err := parseLink(resp)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseLink() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("parseLink() = %v, want %v", got, tt.want)
			}
		})
	}
}
