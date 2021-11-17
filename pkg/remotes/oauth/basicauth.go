package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"

	"golang.org/x/oauth2"
)

func NewBasicAuthTokenSource(ctx context.Context, realm, service, username, password string, scopes string) oauth2.TokenSource {
	src := &basicAuthTokenSource{
		tokenFunc: func() (*oauth2.Token, error) {
			req, err := http.NewRequest("GET", fmt.Sprintf("%s?service=%s&scope=%s", realm, service, scopes), nil)
			if err != nil {
				return nil, err
			}

			if username != "" && password != "" {
				req.SetBasicAuth(username, password)
			} // this is anonymous auth

			c, ok := ctx.Value(oauth2.HTTPClient).(*http.Client)
			if !ok {
				c = http.DefaultClient
			}

			resp, err := c.Do(req)
			if err != nil {
				return nil, err
			}

			defer resp.Body.Close()

			if resp.StatusCode != 200 {
				return nil, fmt.Errorf("basicauth: could not get access token")
			}

			data, err := ioutil.ReadAll(resp.Body)
			if err != nil {
				return nil, err
			}

			token := &oauth2.Token{}
			if err := json.Unmarshal(data, token); err != nil {
				return nil, err
			}

			if token.AccessToken == "" {
				m := make(map[string]string)

				err = json.Unmarshal(data, &m)
				if err != nil {
					return nil, err
				}

				// ghcr.io returns just a single field called token
				t, ok := m["token"]
				if ok {
					token.AccessToken = t
					return token, nil
				}

				return nil, errors.New("basicauth: unrecognized token format")
			}

			return token, nil
		},
	}

	token, err := src.Token()
	if err != nil {
		return nil
	}

	return oauth2.ReuseTokenSource(token, src)
}

type basicAuthTokenSource struct {
	tokenFunc TokenFunc
}

type TokenFunc = func() (*oauth2.Token, error)

func (b basicAuthTokenSource) Token() (*oauth2.Token, error) {
	return b.tokenFunc()
}
