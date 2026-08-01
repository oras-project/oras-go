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

package signature

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/opencontainers/go-digest"
)

// SimpleSigningPayload represents an "atomic container signature" payload
// as defined by the containers/image simple signing format.
// Reference: https://github.com/containers/image/blob/main/docs/atomic-signature.md
type SimpleSigningPayload struct {
	Critical SimpleSigningCritical `json:"critical"`
	Optional SimpleSigningOptional `json:"optional,omitempty"`
}

// SimpleSigningCritical contains the required fields of a simple signing payload.
type SimpleSigningCritical struct {
	Type     string                   `json:"type"`
	Image    SimpleSigningImage       `json:"image"`
	Identity SimpleSigningIdentity    `json:"identity"`
}

// SimpleSigningImage identifies the image being signed.
type SimpleSigningImage struct {
	DockerManifestDigest string `json:"docker-manifest-digest"`
}

// SimpleSigningIdentity identifies the claimed identity of the image.
type SimpleSigningIdentity struct {
	DockerReference string `json:"docker-reference"`
}

// SimpleSigningOptional contains optional metadata in a simple signing payload.
type SimpleSigningOptional struct {
	Creator   string `json:"creator,omitempty"`
	Timestamp int64  `json:"timestamp,omitempty"`
}

const (
	// simpleSigningType is the type field value for simple signing payloads.
	simpleSigningType = "atomic container signature"
)

// ErrInvalidSigningPayload is returned when a simple signing payload is invalid.
var ErrInvalidSigningPayload = errors.New("invalid simple signing payload")

// NewSimpleSigningPayload creates a new simple signing payload for the given
// image digest and docker reference.
func NewSimpleSigningPayload(dgst digest.Digest, dockerReference string) *SimpleSigningPayload {
	return &SimpleSigningPayload{
		Critical: SimpleSigningCritical{
			Type: simpleSigningType,
			Image: SimpleSigningImage{
				DockerManifestDigest: dgst.String(),
			},
			Identity: SimpleSigningIdentity{
				DockerReference: dockerReference,
			},
		},
	}
}

// ParseSimpleSigningPayload parses a simple signing payload from JSON data.
func ParseSimpleSigningPayload(data []byte) (*SimpleSigningPayload, error) {
	var payload SimpleSigningPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidSigningPayload, err)
	}
	return &payload, nil
}

// Marshal serializes the payload to JSON.
func (p *SimpleSigningPayload) Marshal() ([]byte, error) {
	return json.Marshal(p)
}

// Validate checks that the payload has all required fields and is well-formed.
func (p *SimpleSigningPayload) Validate() error {
	if p.Critical.Type != simpleSigningType {
		return fmt.Errorf("%w: unexpected type %q, want %q", ErrInvalidSigningPayload, p.Critical.Type, simpleSigningType)
	}
	if p.Critical.Image.DockerManifestDigest == "" {
		return fmt.Errorf("%w: missing docker-manifest-digest", ErrInvalidSigningPayload)
	}
	if p.Critical.Identity.DockerReference == "" {
		return fmt.Errorf("%w: missing docker-reference", ErrInvalidSigningPayload)
	}
	// Validate that the digest is parseable.
	_, err := digest.Parse(p.Critical.Image.DockerManifestDigest)
	if err != nil {
		return fmt.Errorf("%w: invalid docker-manifest-digest: %v", ErrInvalidSigningPayload, err)
	}
	return nil
}

// ImageDigest returns the parsed image digest from the payload.
func (p *SimpleSigningPayload) ImageDigest() (digest.Digest, error) {
	return digest.Parse(p.Critical.Image.DockerManifestDigest)
}

// DockerReference returns the docker reference from the payload.
func (p *SimpleSigningPayload) DockerReference() string {
	return p.Critical.Identity.DockerReference
}
