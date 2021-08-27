package remotes

import "net/http"

func prepareOAuth2Client(hc *http.Client) {
	// By default golang forwards all headers when it redirects
	// If the url lives under the same subdomain, domain, it will also forward the Auth header
	// I haven't investigated, but it's possible after the header pruning, it gets added back by oauth2 since oauth2 owns the transport
	// To fix this check if the hosts match, and that we aren't deleting a legit Authorization header, for example maybe the next redirect
	// could somehow authenticate somewhere in between. So make sure the header being deleted is the auth header from the previous request

	hc.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > 0 && req.URL.Host != via[0].Host && req.Header.Get("Authorization") == via[0].Header.Get("Authorization") {
			req.Header.Del("Authorization") // if it doesn't exist this is a no-op
			return nil
		}
		return nil
	}
}
