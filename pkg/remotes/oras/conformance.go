package oras

import (
	"github.com/containerd/containerd/remotes"
)

// Ensure the interfaces still match
var (
	_ remotes.Resolver = (*resolver)(nil)
	_ remotes.Fetcher  = (*resolver)(nil)
	_ remotes.Pusher   = (*resolver)(nil)
)
