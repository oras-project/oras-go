package warning

import "net/http"

type client interface {
	Do(req *http.Request) (*http.Response, error)
}

type Client struct {
	Client        client
	HandleWarning func(warning Warning)
}

func (c *Client) client() client {
	if c.Client == nil {
		return http.DefaultClient
	}
	return c.Client
}

func (c *Client) Do(req *http.Request) (*http.Response, error) {
	if c.HandleWarning == nil {
		return c.client().Do(req)
	}

	resp, err := c.client().Do(req)
	if err != nil {
		return nil, err
	}
	// TODO: const
	c.handleWarningHeaders(resp.Header["Warning"])

	return resp, nil
}

func (c *Client) handleWarningHeaders(headers []string) {
	if len(headers) == 0 {
		return
	}

	// TODO: dedup?
	for _, w := range headers {
		if warning, err := parseWarningHeader(w); err == nil {
			c.HandleWarning(warning)
		}
	}
}
