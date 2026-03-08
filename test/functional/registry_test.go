//go:build k8sfunctional

package functional

import (
	"context"
	"fmt"
	"testing"

	"github.com/oras-project/oras-go/v3/registry/remote"
)

func TestRegistryPing(t *testing.T) {
	ctx := context.Background()

	reg, err := remote.NewRegistry(registryEndpoint)
	if err != nil {
		t.Fatalf("NewRegistry failed: %v", err)
	}
	reg.PlainHTTP = true

	if err := reg.Ping(ctx); err != nil {
		t.Fatalf("Ping failed: %v", err)
	}
}

func TestListRepositories(t *testing.T) {
	ctx := context.Background()

	// Push to 3 different repos so they show up in the catalog.
	repoNames := make([]string, 3)
	for i := 0; i < 3; i++ {
		name := fmt.Sprintf("listrepo-%s-%d", uniqueRepoName(t), i)
		repo := newRepo(t, name)
		packAndPush(t, ctx, repo, "latest", nil)
		repoNames[i] = name
	}

	reg, err := remote.NewRegistry(registryEndpoint)
	if err != nil {
		t.Fatalf("NewRegistry failed: %v", err)
	}
	reg.PlainHTTP = true

	var repos []string
	if err := reg.Repositories(ctx, "", func(r []string) error {
		repos = append(repos, r...)
		return nil
	}); err != nil {
		t.Fatalf("Repositories listing failed: %v", err)
	}

	if len(repos) < 3 {
		t.Fatalf("Expected at least 3 repositories, got %d", len(repos))
	}

	repoSet := make(map[string]bool)
	for _, r := range repos {
		repoSet[r] = true
	}
	for _, expected := range repoNames {
		if !repoSet[expected] {
			t.Fatalf("Expected repository %q not found in listed repositories", expected)
		}
	}
}
