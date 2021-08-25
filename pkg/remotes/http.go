package oras

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
)

// Table of endpoints for OCI v2
// end-1	GET			/v2/														200	404/401
// end-2	GET / HEAD	/v2/<name>/blobs/<digest>									200	404
// end-3	GET / HEAD	/v2/<name>/manifests/<reference>							200	404
// end-4a	POST		/v2/<name>/blobs/uploads/									202	404
// end-4b	POST		/v2/<name>/blobs/uploads/?digest=<digest>					201/202	404/400
// end-5	PATCH		/v2/<name>/blobs/uploads/<reference>						202	404/416
// end-6	PUT			/v2/<name>/blobs/uploads/<reference>?digest=<digest>		201	404/400
// end-7	PUT			/v2/<name>/manifests/<reference>							201	404
// end-8a	GET			/v2/<name>/tags/list										200	404
// end-8b	GET			/v2/<name>/tags/list?n=<integer>&last=<integer>				200	404
// end-9	DELETE		/v2/<name>/manifests/<reference>							202	404/400/405
// end-10	DELETE		/v2/<name>/blobs/<digest>									202	404/405
// end-11	POST		/v2/<name>/blobs/uploads/?mount=<digest>&from=<other_name>	201	404

// 	# Value conformance
// <name>		- is the namespace of the repository, must match [a-z0-9]+([._-][a-z0-9]+)*(/[a-z0-9]+([._-][a-z0-9]+)*)*
// <reference>  - is either a digest or a tag, must match [a-zA-Z0-9_][a-zA-Z0-9._-]{0,127}

func validateNamespace(namespace string) bool {
	re := regexp.MustCompile(`[a-z0-9]+([._-][a-z0-9]+)*(/[a-z0-9]+([._-][a-z0-9]+)*)*`)

	return re.FindString(namespace) == namespace
}

func validateReference(reference string) bool {
	re := regexp.MustCompile(`[a-zA-Z0-9_][a-zA-Z0-9._-]{0,127}`)

	return re.FindString(reference) == reference
}

// Resolving & Fetching
// end-1	GET			/v2/														200	404/401
// end-2	GET / HEAD	/v2/<name>/blobs/<digest>									200	404
// end-3	GET / HEAD	/v2/<name>/manifests/<reference>							200	404
// end-8a	GET			/v2/<name>/tags/list										200	404
// end-8b	GET			/v2/<name>/tags/list?n=<integer>&last=<integer>				200	404

const (
	userAgent          string = "pkg/oras-go"
	manifetV2json      string = "manifest.v2+json"
	manifestlistV2json string = "manifest.list.v2+json"
	end2APIFormat      string = "/v2/%s/blobs/%s"
	end3APIFormat      string = "/v2/%s/manifests/%s"
	end8aAPIFormat     string = "/v2/%s/tags/list"
	end8bAPIFormat     string = "?n=%d&last=%d"
)

type req struct {
	method string
	format string
	accept string
}

func (r req) prepare() func(context.Context, string, string, string) (*http.Request, error) {
	return func(c context.Context, host, ns, ref string) (*http.Request, error) {
		path := fmt.Sprintf(r.format, ns, ref)
		url, err := url.Parse("https://" + host + path)
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(c, r.method, url.String(), nil)
		if err != nil {
			return nil, err
		}

		req.Header.Add("Accept", r.accept)

		return req, nil
	}
}

// func (r req) prepareWithRangeQuery(start, end int) func(context.Context, string, string, string) (*http.Request, error) {
// 	return func(c context.Context, host, ns, ref string) (*http.Request, error) {
// 		path := fmt.Sprintf(r.format, ns, ref, start, end)
// 		url, err := url.Parse("https://" + host + path)
// 		if err != nil {
// 			return nil, err
// 		}

// 		req, err := http.NewRequestWithContext(c, r.method, url.String(), nil)
// 		if err != nil {
// 			return nil, err
// 		}

// 		req.Header.Add("Accept", r.accept)

// 		return req, nil
// 	}
// }

var endpoints = struct {
	e1     req
	e2HEAD req
	e2GET  req
	e3HEAD req
	e3GET  req
	e8a    req
	e8b    req
}{
	req{"GET", "v2", manifetV2json},
	req{"HEAD", end2APIFormat, manifetV2json},
	req{"GET", end2APIFormat, manifetV2json},
	req{"HEAD", end3APIFormat, manifetV2json},
	req{"GET", end3APIFormat, manifetV2json},
	req{"GET", end8aAPIFormat, manifetV2json},
	req{"GET", end8bAPIFormat + end8bAPIFormat, manifetV2json},
}

func newHttpClient() *http.Client {
	// TODO fix this later
	return http.DefaultClient
}

// Error & Validation

// Format of an error response
// {
// 	"errors": [
// 		{
// 			"code": "<error identifier, see below>",
// 			"message": "<message describing condition>",
// 			"detail": "<unstructured>"
// 		},
// 		...
// 	]
// }

// code-1	BLOB_UNKNOWN			blob unknown to registry
// code-2	BLOB_UPLOAD_INVALID		blob upload invalid
// code-3	BLOB_UPLOAD_UNKNOWN		blob upload unknown to registry
// code-4	DIGEST_INVALID			provided digest did not match uploaded content
// code-5	MANIFEST_BLOB_UNKNOWN	blob unknown to registry
// code-6	MANIFEST_INVALID		manifest invalid
// code-7	MANIFEST_UNKNOWN		manifest unknown
// code-8	NAME_INVALID			invalid repository name
// code-9	NAME_UNKNOWN			repository name not known to registry
// code-10	SIZE_INVALID			provided length did not match content length
// code-12	UNAUTHORIZED			authentication required
// code-13	DENIED					requested access to the resource is denied
// code-14	UNSUPPORTED				the operation is unsupported
// code-15	TOOMANYREQUESTS			too many requests
