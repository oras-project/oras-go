module oras.land/oras-go/examples

go 1.16

replace oras.land/oras-go/pkg => ../pkg

require (
	github.com/opencontainers/image-spec v1.0.2
	github.com/sirupsen/logrus v1.8.1
	github.com/spf13/cobra v1.0.0
	oras.land/oras-go/pkg v0.0.0-00010101000000-000000000000
)
