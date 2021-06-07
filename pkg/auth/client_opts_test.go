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
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/suite"
)

type ClientOptsSuite struct {
	suite.Suite
}

func (suite *ClientOptsSuite) TestWithLoginContext() {
	settings := &LoginSettings{}
	suite.Nil(settings.Context, "settings.Context is nil by default")

	ctx := context.Background()
	opt := WithLoginContext(ctx)
	opt(settings)
	suite.Equal(ctx, settings.Context, "Able to override settings.Context")
}

func (suite *ClientOptsSuite) TestWithLoginHostname() {
	settings := &LoginSettings{}
	suite.Equal("", settings.Hostname, "settings.Hostname is empty string by default")

	hostname := "example.com"
	opt := WithLoginHostname(hostname)
	opt(settings)
	suite.Equal(hostname, settings.Hostname, "Able to override settings.Hostname")
}

func (suite *ClientOptsSuite) TestWithLoginUsername() {
	settings := &LoginSettings{}
	suite.Equal("", settings.Username, "settings.Username is empty string by default")

	username := "fran"
	opt := WithLoginUsername(username)
	opt(settings)
	suite.Equal(username, settings.Username, "Able to override settings.Username")
}

func (suite *ClientOptsSuite) TestWithLoginSecret() {
	settings := &LoginSettings{}
	suite.Equal("", settings.Secret, "settings.Secret is empty string by default")

	secret := "shhhhhhhhhh"
	opt := WithLoginSecret(secret)
	opt(settings)
	suite.Equal(secret, settings.Secret, "Able to override settings.Secret")
}

func (suite *ClientOptsSuite) TestWithLoginInsecure() {
	settings := &LoginSettings{}
	suite.Equal(false, settings.Insecure, "settings.Insecure is false by default")

	opt := WithLoginInsecure()
	opt(settings)
	suite.Equal(true, settings.Insecure, "Able to override settings.Insecure")
}

func (suite *ClientOptsSuite) TestWithLoginUserAgent() {
	settings := &LoginSettings{}
	suite.Equal("", settings.UserAgent, "settings.UserAgent is empty string by default")

	userAgent := "superclient"
	opt := WithLoginUserAgent(userAgent)
	opt(settings)
	suite.Equal(userAgent, settings.UserAgent, "Able to override settings.UserAgent")
}

func (suite *ClientOptsSuite) TestWithResolverClient() {
	settings := &ResolverSettings{}
	suite.Nil(settings.Client, "settings.Client is nil by default")

	defaultClient := http.DefaultClient
	opt := WithResolverClient(defaultClient)
	opt(settings)
	suite.Equal(defaultClient, settings.Client, "Able to override settings.Client")
}

func (suite *ClientOptsSuite) TestWithResolverPlainHTTP() {
	settings := &ResolverSettings{}
	suite.Equal(false, settings.PlainHTTP, "settings.PlainHTTP is false by default")

	plainHTTP := true
	opt := WithResolverPlainHTTP()
	opt(settings)
	suite.Equal(plainHTTP, settings.PlainHTTP, "Able to override settings.PlainHTTP")
}

func (suite *ClientOptsSuite) TestWithResolverHeaders() {
	settings := &ResolverSettings{}
	suite.Nil(settings.Headers, "settings.Headers is nil by default")

	key := "User-Agent"
	value := "oras-go/test"
	headers := http.Header{}
	headers.Set(key, value)
	opt := WithResolverHeaders(headers)
	opt(settings)
	suite.Equal(settings.Headers.Get(key), value, "Able to override settings.Headers")
}

func TestClientOptsSuite(t *testing.T) {
	suite.Run(t, new(ClientOptsSuite))
}
