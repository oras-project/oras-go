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
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/reference"
	"github.com/containerd/containerd/remotes"
	"github.com/containerd/containerd/remotes/docker"
	"github.com/docker/docker/errdefs"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	artifactspec "github.com/oras-project/artifacts-spec/specs-go/v1"
	"github.com/pkg/errors"
	orasremotesclient "oras.land/oras-go/pkg/remotes"
	"oras.land/oras-go/pkg/target"
)

// WithDiscover extends an existing resolver to include the discoverer interface in the underlying type
func WithDiscover(ref string, resolver remotes.Resolver) (target.Target, error) {
	opts := NewOpts(nil)

	r, err := reference.Parse(ref)
	if err != nil {
		return nil, err
	}

	return &dockerDiscoverer{

		header:     opts.Headers,
		hosts:      opts.Hosts,
		refspec:    r,
		reference:  ref,
		repository: strings.TrimPrefix(r.Locator, r.Hostname()+"/"),
		tracker:    docker.NewInMemoryTracker(),
		Resolver:   resolver}, nil
}

// WithDiscover extends an existing resolver to include the discoverer interface in the underlying type
func FromRemotesRegistry(ref string, client *orasremotesclient.Registry, fallback remotes.Resolver) (target.Target, error) {
	opts := NewOpts(nil)

	r, err := reference.Parse(ref)
	if err != nil {
		return nil, err
	}

	return &dockerDiscoverer{
		client:     client,
		header:     opts.Headers,
		hosts:      opts.Hosts,
		refspec:    r,
		reference:  ref,
		repository: strings.TrimPrefix(r.Locator, r.Hostname()+"/"),
		tracker:    docker.NewInMemoryTracker(),
		Resolver:   fallback}, nil
}

// Discoverer is an interface that provides methods for discovering references
type Discoverer interface {
	// Discover is a function that looks for references of the specified artifact type who share the subject descriptor as their root
	// if artifact type is left blank then all artifactTypes will be searched
	Discover(ctx context.Context, subject ocispec.Descriptor, artifactType string) ([]artifactspec.Descriptor, error)
}

type dockerDiscoverer struct {
	remotes.Resolver
	client     *orasremotesclient.Registry
	hosts      docker.RegistryHosts
	header     http.Header
	refspec    reference.Spec
	reference  string
	repository string
	tracker    docker.StatusTracker
}

// Discover is a function that returns all artifacts that reference the target subject
// The following process is involved in the discover function
// First the an artifact manifest is found whose contents container an array of "Blobs" (analagous to target.Object)
// Contents of the array can point to additional manifests  and so on and so forth
// The result is a graph of Objects that start from the subject, and whose leaves will be artifacts
func (d *dockerDiscoverer) Discover(ctx context.Context, subject ocispec.Descriptor, artifactType string) ([]artifactspec.Descriptor, error) {
	ctx = log.WithLogger(ctx, log.G(ctx).WithField("digest", subject.Digest))
	hosts, err := d.filterHosts(docker.HostCapabilityResolve)
	if err != nil {
		return nil, err
	}

	if len(hosts) == 0 {
		return nil, errdefs.NotFound(errors.New("no discover hosts"))
	}

	ctx, err = docker.ContextWithRepositoryScope(ctx, d.refspec, false)
	if err != nil {
		return nil, err
	}

	var firstErr error
	for _, originalHost := range hosts {
		url, err := d.FormatReferrersAPI(originalHost, subject.Digest.String(), artifactType)
		if err != nil {
			return nil, err
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}

		req.Header.Set("Accept", subject.MediaType)

		refs, err := d.discover(ctx, req)
		if err != nil {
			// Store the error for referencing later
			if firstErr == nil {
				firstErr = err
			}
			continue // try another host
		}

		return refs, nil
	}

	return nil, firstErr
}

var localhostRegex = regexp.MustCompile(`(?:^localhost)|(?:^localhost:[0-9]{1,5})|(?:^127\.0\.0\.1)|(?:^127\.0\.0\.1:\\d{0,5})`)

func (d *dockerDiscoverer) filterHosts(caps docker.HostCapabilities) (hosts []docker.RegistryHost, err error) {
	h, err := d.hosts(d.refspec.Hostname())
	if err != nil {
		return nil, err
	}

	for _, host := range h {
		if host.Capabilities.Has(caps) || localhostRegex.MatchString(host.Host) {
			hosts = append(hosts, host)
		}
	}

	return hosts, nil
}

func (d *dockerDiscoverer) discover(ctx context.Context, req *http.Request) ([]artifactspec.Descriptor, error) {
	resp, err := d.client.Do(ctx, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var registryErr docker.Errors
		if err := json.NewDecoder(resp.Body).Decode(&registryErr); err != nil || registryErr.Len() < 1 {
			return nil, errors.Errorf("unexpected status code %v: %v", req.URL.String(), resp.Status)
		}
		return nil, errors.Errorf("unexpected status code %v: %s - Server message: %s", req.URL.String(), resp.Status, registryErr.Error())
	}

	result := &struct {
		References []artifactspec.Descriptor `json:"references"`
	}{}
	if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
		return nil, err
	}

	return result.References, nil
}
