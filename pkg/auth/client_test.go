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

package auth

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/suite"
)

type ClientSuite struct {
	suite.Suite
}

func (suite *ClientSuite) TestResolverOptClient() {
	settings := &ResolverSettings{}
	suite.Nil(settings.Client, "settings.Client is nil by default")

	defaultClient := http.DefaultClient
	opt := ResolverOptClient(defaultClient)
	opt(settings)
	suite.Equal(defaultClient, settings.Client, "Able to override settings.Client")
}

func (suite *ClientSuite) TestResolverOptPlainHTTP() {
	settings := &ResolverSettings{}
	suite.Equal(false, settings.PlainHTTP, "settings.PlainHTTP is false by default")

	plainHTTP := true
	opt := ResolverOptPlainHTTP(plainHTTP)
	opt(settings)
	suite.Equal(plainHTTP, settings.PlainHTTP, "Able to override settings.PlainHTTP")
}

func (suite *ClientSuite) TestResolverOptHeaders() {
	settings := &ResolverSettings{}
	suite.Nil(settings.Headers, "settings.Headers is nil by default")

	key := "User-Agent"
	value := "oras-go/test"
	headers := http.Header{}
	headers.Set(key, value)
	opt := ResolverOptHeaders(headers)
	opt(settings)
	suite.Equal(settings.Headers.Get(key), value, "Able to override settings.Headers")
}

func TestClientSuite(t *testing.T) {
	suite.Run(t, new(ClientSuite))
}
