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
	"testing"

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

// benchTrackerNodes is the number of distinct descriptors each TryCommit
// benchmark iteration walks. Fixing it keeps the reported ns/op independent
// of b.N and bounds the memory the benchmark holds.
const benchTrackerNodes = 1024

// benchDescriptors builds benchTrackerNodes distinct descriptors. Only the
// fields descriptor.FromOCI keys on need to differ.
func benchDescriptors() []ocispec.Descriptor {
	descs := make([]ocispec.Descriptor, benchTrackerNodes)
	for i := range descs {
		descs[i] = ocispec.Descriptor{Size: int64(i)}
	}
	return descs
}

// BenchmarkTracker_TryCommit_Miss measures first commits, where a channel
// allocation is required. This is the legitimate allocation that must not
// be removed.
func BenchmarkTracker_TryCommit_Miss(b *testing.B) {
	descs := benchDescriptors()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tracker := NewTracker()
		for _, desc := range descs {
			if _, committed := tracker.TryCommit(desc); !committed {
				b.Fatal("TryCommit() should commit on a new descriptor")
			}
		}
	}
}

// BenchmarkTracker_TryCommit_Hit measures repeat calls on already-committed
// descriptors, which must not allocate a channel only to throw it away. This
// is the shape of copyGraph's wait loop in copy.go, which calls TryCommit on
// every already-committed successor purely to fetch its notification channel.
func BenchmarkTracker_TryCommit_Hit(b *testing.B) {
	descs := benchDescriptors()
	tracker := NewTracker()
	for _, desc := range descs {
		if _, committed := tracker.TryCommit(desc); !committed {
			b.Fatal("TryCommit() first call should commit")
		}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, desc := range descs {
			if _, committed := tracker.TryCommit(desc); committed {
				b.Fatal("TryCommit() should not commit on an already-committed descriptor")
			}
		}
	}
}

// BenchmarkTracker_TryCommit_Parallel measures concurrent repeat calls, the
// way copyGraph and Memory.IndexAll actually call TryCommit.
func BenchmarkTracker_TryCommit_Parallel(b *testing.B) {
	descs := benchDescriptors()
	tracker := NewTracker()
	for _, desc := range descs {
		if _, committed := tracker.TryCommit(desc); !committed {
			b.Fatal("TryCommit() first call should commit")
		}
	}

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for i := 0; pb.Next(); i++ {
			tracker.TryCommit(descs[i%len(descs)])
		}
	})
}
