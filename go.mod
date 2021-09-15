module oras.land/oras-go

go 1.16

// WARNING! Do NOT replace these without also replacing their lines in the `require` stanza below.
// These `replace` stanzas are IGNORED when this is imported as a library
replace github.com/docker/docker => github.com/moby/moby v20.10.8+incompatible

require (
	github.com/containerd/containerd v1.5.5
	github.com/distribution/distribution/v3 v3.0.0-20210826081326-677772e08d64
	github.com/docker/cli v20.10.8+incompatible
	github.com/docker/docker v20.10.8+incompatible
	github.com/opencontainers/go-digest v1.0.0
	github.com/opencontainers/image-spec v1.0.1
	github.com/phayes/freeport v0.0.0-20180830031419-95f893ade6f2
	github.com/pkg/errors v0.9.1
	github.com/sirupsen/logrus v1.8.1
	github.com/spf13/cobra v1.0.0
	github.com/stretchr/testify v1.7.0
	golang.org/x/crypto v0.0.0-20210817164053-32db794688a5
	golang.org/x/sync v0.0.0-20210220032951-036812b2e83c
)

require (
	github.com/docker/docker-credential-helpers v0.6.4 // indirect
	github.com/docker/go-connections v0.4.0 // indirect
	github.com/morikuni/aec v1.0.0 // indirect
	github.com/oras-project/artifacts-spec v0.0.0-20210914235636-eecc5d95bcee
	golang.org/x/net v0.0.0-20210226172049-e18ecbb05110
)
