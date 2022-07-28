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

// Package auth_test includes the testable examples for the http client.
package auth_test

const (
	username     = "test_user"
	password     = "test_password"
	accessToken  = "test/access/token"
	refreshToken = "test/refresh/token"
)

var (
	host                  string
	expectedHostAddress   string
	targetURL             string
	clientConfigTargetURL string
	basicAuthTargetURL    string
	accessTokenTargetURL  string
	refreshTokenTargetURL string
	tokenScopes           = []string{
		"repository:dst:pull,push",
		"repository:src:pull",
	}
)
