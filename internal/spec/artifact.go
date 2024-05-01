/*
Copyright The ORAS Authors.
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package spec

import ocispec "github.com/opencontainers/image-spec/specs-go/v1"

const (
	// AnnotationArtifactCreated is the annotation key for the date and time on which the artifact was built, conforming to RFC 3339.
	AnnotationArtifactCreated = "org.opencontainers.artifact.created"

	// AnnotationArtifactDescription is the annotation key for the human readable description for the artifact.
	AnnotationArtifactDescription = "org.opencontainers.artifact.description"

	// AnnotationReferrersFiltersApplied is the annotation key for the comma separated list of filters applied by the registry in the referrers listing.
	AnnotationReferrersFiltersApplied = "org.opencontainers.referrers.filtersApplied"

	// The AnnotationResume* constants define the keys used in resumable downloads
	// in the ocispec.Descriptior.Annotations map for tracking resume state.

	// AnnotationResumeDownload is "true" when a resumable download is being attempted.
	AnnotationResumeDownload = "com.salad.image.resume"

	// AnnotationResumeFilename contains the full ingest filename.
	AnnotationResumeFilename = "com.salad.image.resume.filename"

	// AnnotationResumeHash contains a hash.Hash of the existing ingest file
	// suitable for using in the new Verifier to resume download verification.
	AnnotationResumeHash = "com.salad.image.resume.hash"

	// AnnotationResumeOffset contains the resume download offset, aka
	// the size of the existing ingest file.
	AnnotationResumeOffset = "com.salad.image.resume.offset"
)

// MediaTypeArtifactManifest specifies the media type for a content descriptor.
const MediaTypeArtifactManifest = "application/vnd.oci.artifact.manifest.v1+json"

// Artifact describes an artifact manifest.
// This structure provides `application/vnd.oci.artifact.manifest.v1+json` mediatype when marshalled to JSON.
//
// This manifest type was introduced in image-spec v1.1.0-rc1 and was removed in
// image-spec v1.1.0-rc3. It is not part of the current image-spec and is kept
// here for Go compatibility.
//
// Reference: https://github.com/opencontainers/image-spec/pull/999
type Artifact struct {
	// MediaType is the media type of the object this schema refers to.
	MediaType string `json:"mediaType"`

	// ArtifactType is the IANA media type of the artifact this schema refers to.
	ArtifactType string `json:"artifactType"`

	// Blobs is a collection of blobs referenced by this manifest.
	Blobs []ocispec.Descriptor `json:"blobs,omitempty"`

	// Subject (reference) is an optional link from the artifact to another manifest forming an association between the artifact and the other manifest.
	Subject *ocispec.Descriptor `json:"subject,omitempty"`

	// Annotations contains arbitrary metadata for the artifact manifest.
	Annotations map[string]string `json:"annotations,omitempty"`
}
