package artifacts

import (
	"github.com/containerd/containerd/remotes/docker"
	discoverprovider "oras.land/oras-go/pkg/remotes/docker"
	"oras.land/oras-go/pkg/target"
)

func EnableArtifactDiscoveryOn(original target.Target, reference string, dopts *docker.ResolverOptions) (target.Target, error) {
	discoverer, err := discoverprovider.WithDiscover(reference, original, dopts)
	if err != nil {
		return nil, err
	}

	return discoverer, nil
}
