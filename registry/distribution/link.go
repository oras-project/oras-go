package distribution

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// errNoLink is returned by parseLink() when no Link header is present.
var errNoLink = errors.New("no Link header in response")

// parseLink returns the URL of the response's "Link" header, if present.
func parseLink(resp *http.Response) (string, error) {
	link := resp.Header.Get("Link")
	if link == "" {
		return "", errNoLink
	}
	if link[0] != '<' {
		return "", fmt.Errorf("invalid next link %q: missing '<'", link)
	}
	if i := strings.IndexByte(link, '>'); i == -1 {
		return "", fmt.Errorf("invalid next link %q: missing '>'", link)
	} else {
		link = link[1:i]
	}

	linkURL, err := resp.Request.URL.Parse(link)
	if err != nil {
		return "", err
	}
	return linkURL.String(), nil
}
