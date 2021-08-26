package remotes

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
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

var (
	namespaceRegex = regexp.MustCompile(`[a-z0-9]+([._-][a-z0-9]+)*(/[a-z0-9]+([._-][a-z0-9]+)*)*`)
	referenceRegex = regexp.MustCompile(`[a-zA-Z0-9_][a-zA-Z0-9._-]{0,127}`)
)

func validate(reference string) (string, string, string, error) {
	matches := referenceRegex.FindAllString(reference, -1)

	// Technically a namespace is allowed to have "/"'s, while a reference is not allowed to
	// That means if you string match the reference regex, then you should end up with basically the first segment being the host
	// the middle part being the namespace
	// and the last part should be the tag

	if len(matches) == 3 {
		return matches[0], matches[1], matches[2], nil
	}

	host := matches[0]
	namespace := strings.Join(matches[1:len(matches)-1], "")
	ref := matches[len(matches)-1]

	return host, namespace, ref, nil
}

func validateNamespace(namespace string) (string, error) {
	matches := namespaceRegex.FindAllString(namespace, -1)

	if len(matches) <= 0 {
		return "", fmt.Errorf("Either the reference was empty, or it contained no characters")
	}

	root := matches[0]
	// Check to see if it's in a {host}/{image} format, '/' is a valid character for a reference
	matches = referenceRegex.FindAllString(root, 1)
	if len(matches) > 0 {
		return matches[0], nil
	}

	return "", fmt.Errorf("Malformed reference, a reference should be in the form of {host}/{namespace}:{tag}")
}

func validateReference(reference string) (string, error) {
	matches := referenceRegex.FindAllString(reference, -1)

	if len(matches) <= 0 {
		return "", fmt.Errorf("Either the reference was empty, or it contained no characters")
	}

	maybe := matches[len(matches)-1]

	endsWith := strings.HasSuffix(reference, ":"+maybe)
	if endsWith {
		return maybe, nil
	}

	return "", fmt.Errorf("Malformed reference, a reference should be in the form of {host}/{namespace}:{tag}")
}

// Resolving & Fetching
// end-1	GET			/v2/														200	404/401
// end-2	GET / HEAD	/v2/<name>/blobs/<digest>									200	404
// end-3	GET / HEAD	/v2/<name>/manifests/<reference>							200	404
// end-8a	GET			/v2/<name>/tags/list										200	404
// end-8b	GET			/v2/<name>/tags/list?n=<integer>&last=<integer>				200	404

const (
	userAgent          string = "pkg/oras-go"
	manifetV2json      string = "application/vnd.docker.distribution.manifest.v2+json"
	manifestlistV2json string = "application/vnd.docker.distribution.manifest.list.v2+json"
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
		var (
			path string
		)

		// Special case if this is e1 since there are no parameters for that call
		if r.format == endpoints.e1.format {
			path = r.format
		} else {
			path = fmt.Sprintf(r.format, ns, ref)
		}

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

func (r req) prepareWithDescriptor() func(context.Context, string, string, ocispec.Descriptor) (*http.Request, error) {
	return func(c context.Context, host, ns string, desc ocispec.Descriptor) (*http.Request, error) {
		var (
			path string
		)

		// Special case if this is e1 since there are no parameters for that call
		if r.format == endpoints.e1.format {
			path = r.format
		} else {
			path = fmt.Sprintf(r.format, ns, desc.Digest)
		}

		url, err := url.Parse("https://" + host + path)
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(c, r.method, url.String(), nil)
		if err != nil {
			return nil, err
		}

		req.Header.Add("Accept", desc.MediaType)

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
	e3HEAD req
	e3GET  req
	e2HEAD req
	e2GET  req
	e8a    req
	e8b    req
}{
	req{"GET", "/v2", manifetV2json},
	req{"HEAD", end3APIFormat, manifetV2json},
	req{"GET", end3APIFormat, manifetV2json},
	req{"HEAD", end2APIFormat, manifetV2json},
	req{"GET", end2APIFormat, manifetV2json},
	req{"GET", end8aAPIFormat, manifetV2json},
	req{"GET", end8bAPIFormat + end8bAPIFormat, manifetV2json},
}

func newHttpClient() *http.Client {
	client := &http.Client{}
	// See basicauth for details on this
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > 0 && req.URL.Host != via[0].Host && req.Header.Get("Authorization") == via[0].Header.Get("Authorization") {
			req.Header.Del("Authorization") // if it doesn't exist this is a no-op
			return nil
		}
		return nil
	}

	return client
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
