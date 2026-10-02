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

package oras_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3"
	"github.com/oras-project/oras-go/v3/content"
	"github.com/oras-project/oras-go/v3/content/memory"
	"github.com/oras-project/oras-go/v3/errdef"
)

func TestExtendedCopyGraph_DepthWithSharedPredecessor(t *testing.T) {
	ctx := context.Background()
	src := memory.New()
	newIndex := func(name string, manifests ...ocispec.Descriptor) ocispec.Descriptor {
		if manifests == nil {
			manifests = []ocispec.Descriptor{}
		}
		data, err := json.Marshal(ocispec.Index{
			Versioned:   specs.Versioned{SchemaVersion: 2},
			MediaType:   ocispec.MediaTypeImageIndex,
			Manifests:   manifests,
			Annotations: map[string]string{"name": name},
		})
		if err != nil {
			t.Fatal(err)
		}
		desc := content.NewDescriptorFromBytes(ocispec.MediaTypeImageIndex, data)
		if err := src.Push(ctx, desc, bytes.NewReader(data)); err != nil {
			t.Fatal(err)
		}
		return desc
	}
	node := newIndex("node")
	short := newIndex("short", node)
	long := newIndex("long", node)
	middle := newIndex("middle", long)
	shared := newIndex("shared", short, middle)
	root := newIndex("root", shared)
	beyond := newIndex("beyond", root)

	// Depth-first traversal first visits the shared predecessor at depth 3
	// along the long path, then reaches it at depth 2 along the short path.
	// Its own predecessor is within the limit only along the short path.
	for _, tc := range []struct {
		name  string
		first ocispec.Descriptor
		last  ocispec.Descriptor
	}{
		{"long path first", short, long},
		{"short path first", long, short},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dst := memory.New()
			opts := oras.ExtendedCopyGraphOptions{
				Depth: 3,
				FindPredecessors: func(ctx context.Context, src content.ReadOnlyGraphStorage, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
					if desc.Digest == node.Digest {
						return []ocispec.Descriptor{tc.first, tc.last}, nil
					}
					return src.Predecessors(ctx, desc)
				},
			}
			if err := oras.ExtendedCopyGraph(ctx, src, dst, node, opts); err != nil {
				t.Fatal(err)
			}
			if _, err := content.FetchAll(ctx, dst, root); err != nil {
				t.Fatalf("predecessor within depth limit missing: %v", err)
			}
			if _, err := content.FetchAll(ctx, dst, beyond); !errors.Is(err, errdef.ErrNotFound) {
				t.Fatalf("predecessor beyond depth limit: %v", err)
			}
		})
	}
}
