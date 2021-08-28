package remotes

import (
	"context"
	"encoding/json"
	"net/http"
)

type artifacts struct {
	ref          reference
	artifactType string
}

func (a artifacts) discover(ctx context.Context, client *http.Client) (*Artifacts, error) {
	request, err := endpoints.listReferrers.prepareWithArtifactType()(ctx, a.ref.add.host, a.ref.add.ns, a.ref.digst.String(), a.ref.media, a.artifactType)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(request)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	d := &Artifacts{}
	json.NewDecoder(resp.Body).Decode(d)

	return d, nil
}
