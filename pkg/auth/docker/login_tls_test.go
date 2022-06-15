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

package docker

import (
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/distribution/distribution/v3/configuration"
	"github.com/distribution/distribution/v3/registry"
	_ "github.com/distribution/distribution/v3/registry/auth/htpasswd"
	_ "github.com/distribution/distribution/v3/registry/storage/driver/inmemory"
	"github.com/phayes/freeport"
	"github.com/stretchr/testify/suite"
	"golang.org/x/crypto/bcrypt"

	iface "oras.land/oras-go/pkg/auth"
)

const (
	tlsServerKey  = "./testdata/tls/server-key.pem"
	tlsServerCert = "./testdata/tls/server-cert.pem"
	tlsCA         = "./testdata/tls/ca-cert.pem"
	tlsKey        = "./testdata/tls/client-key.pem"
	tlsCert       = "./testdata/tls/client-cert.pem"
)

type DockerClientWithCATestSuite struct {
	suite.Suite
	DockerRegistryHost string
	Client             *Client
	TempTestDir        string
}

func (suite *DockerClientWithCATestSuite) SetupSuite() {
	tempDir, err := ioutil.TempDir("", "oras_auth_docker_test_ca")
	suite.Nil(err, "no error creating temp directory for test")
	suite.TempTestDir = tempDir

	// Create client
	client, err := NewClient(filepath.Join(suite.TempTestDir, testConfig))
	suite.Nil(err, "no error creating client")
	var ok bool
	suite.Client, ok = client.(*Client)
	suite.True(ok, "NewClient returns a *docker.Client inside")

	// Create htpasswd file with bcrypt
	secret, err := bcrypt.GenerateFromPassword([]byte(testPassword), bcrypt.DefaultCost)
	suite.Nil(err, "no error generating bcrypt password for test htpasswd file")
	authRecord := fmt.Sprintf("%s:%s\n", testUsername, string(secret))
	htpasswdPath := filepath.Join(suite.TempTestDir, testHtpasswd)
	err = ioutil.WriteFile(htpasswdPath, []byte(authRecord), 0644)
	suite.Nil(err, "no error creating test htpasswd file")

	// Registry config
	config := &configuration.Configuration{}
	port, err := freeport.GetFreePort()
	suite.Nil(err, "no error finding free port for test registry")
	suite.DockerRegistryHost = fmt.Sprintf("localhost:%d", port)

	// HTTP config
	config.HTTP.Addr = fmt.Sprintf(":%d", port)
	config.HTTP.DrainTimeout = time.Duration(10) * time.Second

	config.Log.Level = "debug"

	// TLS config
	// this set tlsConf.ClientAuth = tls.RequireAndVerifyClientCert in the
	// server tls config
	config.HTTP.TLS.Certificate = tlsServerCert
	config.HTTP.TLS.Key = tlsServerKey
	config.HTTP.TLS.ClientCAs = []string{tlsCA}

	// Storage config
	config.Storage = map[string]configuration.Parameters{"inmemory": map[string]interface{}{}}

	// Auth config
	config.Auth = configuration.Auth{
		"htpasswd": configuration.Parameters{
			"realm": "localhost",
			"path":  htpasswdPath,
		},
	}
	dockerRegistry, err := registry.NewRegistry(context.Background(), config)
	suite.Nil(err, "no error finding free port for test registry")

	// Start Docker registry
	go func() {
		err := dockerRegistry.ListenAndServe()
		suite.Nil(err, "no error starting docker registry with ca")
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			suite.FailNow("docker registry timed out")
		default:
		}
		req, err := http.NewRequestWithContext(
			ctx,
			http.MethodGet,
			fmt.Sprintf("http://%s/v2/", suite.DockerRegistryHost),
			nil,
		)
		suite.Nil(err, "no error in generate a /v2/ request")

		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			resp.Body.Close()
			break
		}
		time.Sleep(time.Second)
	}
}

func (suite *DockerClientWithCATestSuite) TearDownSuite() {
	os.RemoveAll(suite.TempTestDir)
}

func (suite *DockerClientWithCATestSuite) Test_1_LoginWithTLSOpts() {
	var err error

	opts := []iface.LoginOption{
		iface.WithLoginContext(newContext()),
		iface.WithLoginHostname(suite.DockerRegistryHost),
		iface.WithLoginUsername("oscar"),
		iface.WithLoginSecret("opponent"),
		iface.WithLoginTLS(
			tlsCert,
			tlsKey,
			tlsCA,
		),
	}
	err = suite.Client.LoginWithOpts(opts...)
	suite.NotNil(err, "error logging into registry with invalid credentials (LoginWithOpts)")

	opts = []iface.LoginOption{
		iface.WithLoginContext(newContext()),
		iface.WithLoginHostname(suite.DockerRegistryHost),
		iface.WithLoginUsername(testUsername),
		iface.WithLoginSecret(testPassword),
		iface.WithLoginTLS(
			tlsCert,
			tlsKey,
			tlsCA,
		),
	}
	err = suite.Client.LoginWithOpts(opts...)
	suite.Nil(err, "no error logging into registry with valid credentials (LoginWithOpts)")
}

func (suite *DockerClientWithCATestSuite) Test_2_Logout() {
	var err error

	err = suite.Client.Logout(newContext(), "non-existing-host:42")
	suite.NotNil(err, "error logging out of registry that has no entry")

	err = suite.Client.Logout(newContext(), suite.DockerRegistryHost)
	suite.Nil(err, "no error logging out of registry")
}

func TestDockerClientWithCATestSuite(t *testing.T) {
	suite.Run(t, new(DockerClientWithCATestSuite))
}
