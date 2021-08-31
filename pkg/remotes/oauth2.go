package remotes

import "net/http"

type oauth2Handlers struct{}

func (o *oauth2Handlers) CheckRedirect(req *http.Request, via []*http.Request) error {
	if len(via) > 0 && req.URL.Host != via[0].Host && req.Header.Get("Authorization") == via[0].Header.Get("Authorization") {
		req.Header.Del("Authorization") // if it doesn't exist this is a no-op
		return nil
	}
	return nil
}
