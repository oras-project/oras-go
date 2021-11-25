package registry

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/opencontainers/go-digest"
	"oras.land/oras-go/v2/errdef"
)

// regular expressions for components.
var (
	registryRegexp   = regexp.MustCompile(`^(?:[a-zA-Z0-9]|[a-zA-Z0-9][a-zA-Z0-9-]*[a-zA-Z0-9])(?:\.(?:[a-zA-Z0-9]|[a-zA-Z0-9][a-zA-Z0-9-]*[a-zA-Z0-9]))*(?::[0-9]+)?$`)
	repositoryRegexp = regexp.MustCompile(`^[a-z0-9]+(?:(?:[._]|__|[-]*)[a-z0-9]+)*(?:/[a-z0-9]+(?:(?:[._]|__|[-]*)[a-z0-9]+)*)*$`)
	tagRegexp        = regexp.MustCompile(`^[\w][\w.-]{0,127}$`)
)

// Reference references to a descriptor in the registry.
type Reference struct {
	// Registry is the name of the registry.
	// It is usually the domain name of the registry optionally with a port.
	Registry string

	// Repository is the name of the repository.
	Repository string

	// Reference is the reference of the object in the repository.
	// A reference can be a tag or a digest.
	Reference string
}

// ParseReference parses a string into a artifact reference.
// If the reference contains both the tag and the digest, the tag will be
// dropped.
// Digest is recognized only if the corresponding algorithm is available.
func ParseReference(raw string) (Reference, error) {
	parts := strings.SplitN(raw, "/", 2)
	if len(parts) == 1 {
		return Reference{}, errdef.ErrInvalidReference
	}
	registry, path := parts[0], parts[1]
	var repository string
	var reference string
	if index := strings.Index(path, "@"); index != -1 {
		// digest found
		repository = path[:index]
		reference = path[index+1:]

		// drop tag since the digest is present.
		if index := strings.Index(repository, ":"); index != -1 {
			repository = repository[:index]
		}
	} else if index := strings.Index(path, ":"); index != -1 {
		// tag found
		repository = path[:index]
		reference = path[index+1:]
	} else {
		// empty reference
		repository = path
	}
	res := Reference{
		Registry:   registry,
		Repository: repository,
		Reference:  reference,
	}
	if err := res.Validate(); err != nil {
		return Reference{}, err
	}
	return res, nil
}

// Validate validates the reference.
func (r Reference) Validate() error {
	if !registryRegexp.MatchString(r.Registry) {
		return fmt.Errorf("%w: invalid registry", errdef.ErrInvalidReference)
	}
	if !repositoryRegexp.MatchString(r.Repository) {
		return fmt.Errorf("%w: invalid repository", errdef.ErrInvalidReference)
	}
	if r.Reference == "" {
		return nil
	}
	if _, err := r.Digest(); err == nil {
		return nil
	}
	if !tagRegexp.MatchString(r.Reference) {
		return fmt.Errorf("%w: invalid tag", errdef.ErrInvalidReference)
	}
	return nil
}

// Host returns the host name of the registry.
func (r Reference) Host() string {
	if r.Registry == "docker.io" {
		return "registry-1.docker.io"
	}
	return r.Registry
}

// ReferenceOrDefault returns the reference or the default reference if empty.
func (r Reference) ReferenceOrDefault() string {
	if r.Reference == "" {
		return "latest"
	}
	return r.Reference
}

// Digest returns the reference as a digest.
func (r Reference) Digest() (digest.Digest, error) {
	return digest.Parse(r.Reference)
}

// String implements `fmt.Stringer` and returns the reference string.
func (r Reference) String() string {
	ref := r.Registry + "/" + r.Repository
	if r.Reference == "" {
		return ref
	}
	if d, err := r.Digest(); err == nil {
		return ref + "@" + d.String()
	}
	return ref + ":" + r.Reference
}
