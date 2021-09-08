package target

import (
	"github.com/containerd/containerd/remotes"
)

// Target represents a place to which one can send/push or retrieve/pull artifacts.
// Anything that implements the Target interface can be used as a place to send or
// retrieve artifacts.
type Target interface {
	remotes.Resolver
}
