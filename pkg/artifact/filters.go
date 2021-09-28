package artifact

import (
	artifactspec "github.com/oras-project/artifacts-spec/specs-go/v1"
)

type Filter = func(artifactspec.Descriptor) bool

func AnnotationFilter(af func(annotations map[string]string) bool) Filter {
	return func(d artifactspec.Descriptor) bool {
		return af(d.Annotations)
	}
}
