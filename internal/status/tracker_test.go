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

package status

import (
	"fmt"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestTracker_TryCommit(t *testing.T) {
	tracker := NewTracker()
	var desc ocispec.Descriptor

	notify, committed := tracker.TryCommit(desc)
	if !committed {
		t.Fatalf("Tracker.TryCommit() got = %v, want %v", committed, true)
	}

	done, committed := tracker.TryCommit(desc)
	if committed {
		t.Fatalf("Tracker.TryCommit() got = %v, want %v", committed, false)
	}

	done2, committed := tracker.TryCommit(desc)
	if committed {
		t.Fatalf("Tracker.TryCommit() got = %v, want %v", committed, false)
	}

	// status: working in progress
	select {
	case <-done:
		t.Fatalf("unexpected done")
	default:
	}

	select {
	case <-done2:
		t.Fatalf("unexpected done")
	default:
	}

	// mark status as done
	close(notify)

	// status: done
	select {
	case <-done:
	default:
		t.Fatalf("unexpected in progress")
	}

	select {
	case <-done2:
	default:
		t.Fatalf("unexpected in progress")
	}
}

// BenchmarkTracker_TryCommit_Hit measures the case where the descriptor is
// already committed, so the call should not allocate a channel that is
// immediately thrown away. This is the shape of copyGraph's wait loop in
// copy.go, which calls TryCommit on every already-committed successor purely
// to fetch its notification channel.
func BenchmarkTracker_TryCommit_Hit(b *testing.B) {
	tracker := NewTracker()
	var desc ocispec.Descriptor
	if _, committed := tracker.TryCommit(desc); !committed {
		b.Fatal("TryCommit() first call should commit")
	}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, committed := tracker.TryCommit(desc); committed {
			b.Fatal("TryCommit() should not commit on an already-committed descriptor")
		}
	}
}

// BenchmarkTracker_TryCommit_Miss measures the case where each descriptor is
// new, so a channel allocation is required. This is the legitimate
// allocation the fix must not remove.
func BenchmarkTracker_TryCommit_Miss(b *testing.B) {
	tracker := NewTracker()
	descs := make([]ocispec.Descriptor, b.N)
	for i := range descs {
		descs[i] = ocispec.Descriptor{Digest: digest.Digest(fmt.Sprintf("sha256:%064d", i))}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, committed := tracker.TryCommit(descs[i]); !committed {
			b.Fatal("TryCommit() should commit on a new descriptor")
		}
	}
}
