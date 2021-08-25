package oras

import (
	"net/http"
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

// 	# Glossary
// <name>		- is the namespace of the repository, must match [a-z0-9]+([._-][a-z0-9]+)*(/[a-z0-9]+([._-][a-z0-9]+)*)*
// <reference>  - is either a digest or a tag, must match [a-zA-Z0-9_][a-zA-Z0-9._-]{0,127}

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

func newHttpClient() *http.Client {
	// TODO fix this later
	return http.DefaultClient
}

func validateNamespace(namespace string) bool {
	re := regexp.MustCompile(`[a-z0-9]+([._-][a-z0-9]+)*(/[a-z0-9]+([._-][a-z0-9]+)*)*`)

	return re.FindString(namespace) == namespace
}

func validateReference(reference string) bool {
	re := regexp.MustCompile(`[a-zA-Z0-9_][a-zA-Z0-9._-]{0,127}`)

	return re.FindString(reference) == reference
}
