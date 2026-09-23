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
