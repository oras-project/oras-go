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
	"io"
	"net/http"
	"strings"

	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/remotes/docker"
	"github.com/docker/docker/errdefs"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"golang.org/x/net/context/ctxhttp"
)

// request is an internal type to facilitate outgoing http calls to the registry
type request struct {
	method string
	path   string
	header http.Header
	host   docker.RegistryHost
	body   func() (io.ReadCloser, error)
	size   int64
}

// response is an internal type to facilitate incoming http responses from the registry
type response struct {
	*http.Response
	err error
}

func (r *request) authorize(ctx context.Context, req *http.Request) error {
	// Check if has header for host
	if r.host.Authorizer != nil {
		if err := r.host.Authorizer.Authorize(ctx, req); err != nil {
			return err
		}
	}

	return nil
}

func (r *request) do(ctx context.Context) (*http.Response, error) {
	u := r.host.Scheme + "://" + r.host.Host + r.path
	req, err := http.NewRequest(r.method, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header = http.Header{} // headers need to be copied to avoid concurrent map access
	for k, v := range r.header {
		req.Header[k] = v
	}
	if r.body != nil {
		body, err := r.body()
		if err != nil {
			return nil, err
		}
		req.Body = body
		req.GetBody = r.body
		if r.size > 0 {
			req.ContentLength = r.size
		}
	}

	ctx = log.WithLogger(ctx, log.G(ctx).WithField("url", u))
	log.G(ctx).WithFields(requestFields(req)).Debug("do request")
	if err := r.authorize(ctx, req); err != nil {
		return nil, errors.Wrap(err, "failed to authorize")
	}

	var client = &http.Client{}
	if r.host.Client != nil {
		*client = *r.host.Client
	}
	if client.CheckRedirect == nil {
		client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			return errors.Wrap(r.authorize(ctx, req), "failed to authorize redirect")
		}
	}

	resp, err := ctxhttp.Do(ctx, client, req)
	if err != nil {
		return nil, errors.Wrap(err, "failed to do request")
	}
	log.G(ctx).WithFields(responseFields(resp)).Debug("fetch response received")
	return resp, nil
}

func (r *request) doWithRetries(ctx context.Context, responses []*http.Response) (*http.Response, error) {
	resp, err := r.do(ctx)
	if err != nil {
		return nil, err
	}

	responses = append(responses, resp)
	retry, err := r.retryRequest(ctx, responses)
	if err != nil {
		resp.Body.Close()
		return nil, err
	}
	if retry {
		resp.Body.Close()
		return r.doWithRetries(ctx, responses)
	}
	return resp, err
}

func (r *request) retryRequest(ctx context.Context, responses []*http.Response) (bool, error) {
	if len(responses) > 5 {
		return false, nil
	}
	last := responses[len(responses)-1]
	switch last.StatusCode {
	case http.StatusUnauthorized:
		log.G(ctx).WithField("header", last.Header.Get("WWW-Authenticate")).Debug("Unauthorized")
		if r.host.Authorizer != nil {
			if err := r.host.Authorizer.AddResponses(ctx, responses); err == nil {
				return true, nil
			} else if !errdefs.IsNotImplemented(err) {
				return false, err
			}
		}

		return false, nil
	case http.StatusMethodNotAllowed:
		// Support registries which have not properly implemented the HEAD method for
		// manifests endpoint
		if r.method == http.MethodHead && strings.Contains(r.path, "/manifests/") {
			r.method = http.MethodGet
			return true, nil
		}
	case http.StatusRequestTimeout, http.StatusTooManyRequests:
		return true, nil
	}

	// TODO: Handle 50x errors accounting for attempt history
	return false, nil
}

func (r *request) String() string {
	return r.host.Scheme + "://" + r.host.Host + r.path
}

func requestFields(req *http.Request) logrus.Fields {
	fields := map[string]interface{}{
		"request.method": req.Method,
	}
	for k, vals := range req.Header {
		k = strings.ToLower(k)
		for i, v := range vals {
			field := "request.header." + k
			if i > 0 {
				field = fmt.Sprintf("%s.%d", field, i)
			}

			switch k {
			case "authorization":
				fields[field] = "****"
			default:
				fields[field] = v
			}
		}
	}

	return logrus.Fields(fields)
}

func responseFields(resp *http.Response) logrus.Fields {
	fields := map[string]interface{}{
		"response.status": resp.Status,
	}
	for k, vals := range resp.Header {
		k = strings.ToLower(k)
		for i, v := range vals {
			field := "response.header." + k
			if i > 0 {
				field = fmt.Sprintf("%s.%d", field, i)
			}
			fields[field] = v
		}
	}

	return logrus.Fields(fields)
}
