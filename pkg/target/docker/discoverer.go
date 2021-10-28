/*
   Copyright The containerd Authors.

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
	"net/url"
	"path"
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

type Discoverer interface {
	Discover(ctx context.Context, subject ocispec.Descriptor, artifactType string) ([]artifactspec.Descriptor, error)
}

type dockerDiscoverer struct {
	hosts      docker.RegistryHosts
	header     http.Header
	refspec    reference.Spec
	reference  string
	repository string
	tracker    docker.StatusTracker
	remotes.Resolver
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

	v := url.Values{}
	v.Set("artifactType", artifactType)
	query := "?" + v.Encode()

	var firstErr error
	for _, originalHost := range hosts {
		host := originalHost
		host.Path = strings.TrimSuffix(host.Path, "/v2") + "/oras/artifacts/v1"

		req := d.request(host, http.MethodGet, "manifests", subject.Digest.String(), "referrers")
		req.path += query
		if err := req.addNamespace(d.refspec.Hostname()); err != nil {
			return nil, err
		}

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

var localhostRegex = regexp.MustCompile(`(?:^localhost$)|(?:^localhost:\\d{0,5}$)|(?:^127\.0\.0\.1$)`)

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

func (d *dockerDiscoverer) discover(ctx context.Context, req *request) ([]artifactspec.Descriptor, error) {
	resp, err := req.doWithRetries(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var registryErr docker.Errors
		if err := json.NewDecoder(resp.Body).Decode(&registryErr); err != nil || registryErr.Len() < 1 {
			return nil, errors.Errorf("unexpected status code %v: %v", req.String(), resp.Status)
		}
		return nil, errors.Errorf("unexpected status code %v: %s - Server message: %s", req.String(), resp.Status, registryErr.Error())
	}

	result := &struct {
		References []artifactspec.Descriptor `json:"references"`
	}{}
	if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
		return nil, err
	}

	return result.References, nil
}

func isProxy(myhost, refhost string) bool {
	if refhost != myhost {
		if refhost != "docker.io" || myhost != "registry-1.docker.io" {
			return true
		}
	}
	return false
}

func (r *request) addNamespace(ns string) (err error) {
	if !isProxy(r.host.Host, ns) {
		return nil
	}
	var q url.Values
	// Parse query
	if i := strings.IndexByte(r.path, '?'); i > 0 {
		r.path = r.path[:i+1]
		q, err = url.ParseQuery(r.path[i+1:])
		if err != nil {
			return
		}
	} else {
		r.path = r.path + "?"
		q = url.Values{}
	}
	q.Add("ns", ns)

	r.path = r.path + q.Encode()

	return
}

func (d *dockerDiscoverer) request(host docker.RegistryHost, method string, ps ...string) *request {
	header := d.header.Clone()
	if header == nil {
		header = http.Header{}
	}

	for key, value := range host.Header {
		header[key] = append(header[key], value...)
	}
	parts := append([]string{"/", host.Path, d.repository}, ps...)
	p := path.Join(parts...)
	// Join strips trailing slash, re-add ending "/" if included
	if len(parts) > 0 && strings.HasSuffix(parts[len(parts)-1], "/") {
		p = p + "/"
	}
	return &request{
		method: method,
		path:   p,
		header: header,
		host:   host,
	}
}
