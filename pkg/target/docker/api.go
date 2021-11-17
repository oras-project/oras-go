package docker

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/containerd/containerd/remotes/docker"
)

func (d *dockerDiscoverer) FormatManifestAPI(host docker.RegistryHost, digest string) (string, error) {
	uri := fmt.Sprintf("%s://%s%s/%s/manifests/%s", host.Scheme, host.Host, host.Path, d.repository, digest)

	url, err := url.Parse(uri)
	if err != nil {
		return "", err
	}

	return url.String(), nil
}

func (d *dockerDiscoverer) FormatReferrersAPI(host docker.RegistryHost, digest, artifactType string) (string, error) {
	host.Path = strings.TrimSuffix(host.Path, "/v2") + "/oras/artifacts/v1"
	// Check if the manifest exists
	uri := fmt.Sprintf("%s://%s%s/%s/manifests/%s/referrers", host.Scheme, host.Host, host.Path, d.repository, digest)

	url, err := url.Parse(uri)
	if err != nil {
		return "", err
	}

	if artifactType != "" {
		query := url.Query()
		query.Add("artifactType", artifactType)

		url.RawQuery = query.Encode()
	}

	return url.String(), nil
}

func (d *dockerDiscoverer) FormatBlobAPI(host docker.RegistryHost, digest string) (string, error) {
	// Check if the manifest exists
	uri := fmt.Sprintf("%s://%s%s/%s/blobs/%s", host.Scheme, host.Host, host.Path, d.repository, digest)

	url, err := url.Parse(uri)
	if err != nil {
		return "", err
	}

	return url.String(), nil
}
