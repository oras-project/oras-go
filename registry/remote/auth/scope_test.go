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

package auth

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/oras-project/oras-go/v3/errdef"
	"github.com/oras-project/oras-go/v3/registry/remote/properties"
)

func TestParseScope(t *testing.T) {
	tests := []struct {
		name  string
		scope string
		want  Scope
	}{
		{
			name:  "single action",
			scope: "repository:foo:pull",
			want:  Scope{ResourceType: "repository", ResourceName: "foo", Actions: []string{"pull"}},
		},
		{
			name:  "multiple actions",
			scope: "repository:foo:pull,push",
			want:  Scope{ResourceType: "repository", ResourceName: "foo", Actions: []string{"pull", "push"}},
		},
		{
			name:  "unordered and duplicated actions",
			scope: "repository:foo:push,pull,push",
			want:  Scope{ResourceType: "repository", ResourceName: "foo", Actions: []string{"pull", "push"}},
		},
		{
			name:  "wildcard absorbs the other actions",
			scope: "repository:foo:pull,*,push",
			want:  Scope{ResourceType: "repository", ResourceName: "foo", Actions: []string{"*"}},
		},
		{
			name:  "registry catalog",
			scope: "registry:catalog:*",
			want:  Scope{ResourceType: "registry", ResourceName: "catalog", Actions: []string{"*"}},
		},
		{
			name:  "namespaced repository",
			scope: "repository:namespace/foo:pull",
			want:  Scope{ResourceType: "repository", ResourceName: "namespace/foo", Actions: []string{"pull"}},
		},
		{
			name:  "resource name containing a colon",
			scope: "repository:foo:bar:pull",
			want:  Scope{ResourceType: "repository", ResourceName: "foo:bar", Actions: []string{"pull"}},
		},
		{
			name:  "empty action in the action list is dropped",
			scope: "repository:foo:pull,,push",
			want:  Scope{ResourceType: "repository", ResourceName: "foo", Actions: []string{"pull", "push"}},
		},
		{
			// public.ecr.aws challenges with this
			name:  "opaque scope",
			scope: "aws",
			want:  Scope{ResourceType: "aws"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseScope(tt.scope)
			if err != nil {
				t.Fatal("ParseScope() error =", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseScope() = %v, want %v", got, tt.want)
			}
			// a parsed scope round trips through its wire form
			if roundTrip := got.String(); roundTrip != tt.scope {
				if reParsed, err := ParseScope(roundTrip); err != nil || !reflect.DeepEqual(reParsed, got) {
					t.Errorf("ParseScope(%q.String()) = %v, %v, want %v", tt.scope, reParsed, err, got)
				}
			}
		})
	}
}

func TestParseScope_invalid(t *testing.T) {
	tests := []struct {
		name  string
		scope string
	}{
		{name: "empty", scope: ""},
		{name: "one colon", scope: "invalid:scope"},
		{name: "no action", scope: "no:actions:"},
		{name: "only empty actions", scope: "repository:foo:,"},
		{name: "no resource type", scope: ":foo:pull"},
		{name: "no resource name", scope: "repository::pull"},
		{name: "space in resource name", scope: "repository:foo bar:pull"},
		{name: "space in action", scope: "repository:foo:pull push"},
		{name: "tab in resource name", scope: "repository:foo\tbar:pull"},
		{name: "newline in action", scope: "repository:foo:pull\npush"},
		{name: "comma in resource type", scope: "repo,sitory:foo:pull"},
		{name: "comma in resource name", scope: "repository:foo,bar:pull"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseScope(tt.scope)
			if !errors.Is(err, errdef.ErrInvalidScope) {
				t.Errorf("ParseScope(%q) error = %v, want %v", tt.scope, err, errdef.ErrInvalidScope)
			}
			if !reflect.DeepEqual(got, Scope{}) {
				t.Errorf("ParseScope(%q) = %v, want the zero Scope", tt.scope, got)
			}
		})
	}
}

func TestScope_String(t *testing.T) {
	tests := []struct {
		name  string
		scope Scope
		want  string
	}{
		{
			name: "zero scope",
		},
		{
			name:  "no resource type",
			scope: Scope{ResourceName: "foo", Actions: []string{"pull"}},
		},
		{
			name:  "no resource name",
			scope: Scope{ResourceType: "repository", Actions: []string{"pull"}},
		},
		{
			name:  "no actions",
			scope: Scope{ResourceType: "repository", ResourceName: "foo"},
		},
		{
			name:  "opaque scope",
			scope: Scope{ResourceType: "aws"},
			want:  "aws",
		},
		{
			name:  "single action",
			scope: Scope{ResourceType: "repository", ResourceName: "foo", Actions: []string{"pull"}},
			want:  "repository:foo:pull",
		},
		{
			name:  "multiple actions",
			scope: Scope{ResourceType: "repository", ResourceName: "foo", Actions: []string{"pull", "push"}},
			want:  "repository:foo:pull,push",
		},
		{
			name:  "registry catalog",
			scope: ScopeRegistryCatalog(),
			want:  "registry:catalog:*",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.scope.String(); got != tt.want {
				t.Errorf("Scope.String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestScopeRepository(t *testing.T) {
	tests := []struct {
		name       string
		repository string
		actions    []string
		want       Scope
	}{
		{
			name: "empty repository",
			actions: []string{
				"pull",
			},
		},
		{
			name:       "nil actions",
			repository: "foo",
		},
		{
			name:       "empty actions list",
			repository: "foo",
			actions:    []string{},
		},
		{
			name:       "empty action",
			repository: "foo",
			actions: []string{
				"",
			},
		},
		{
			name:       "single action",
			repository: "foo",
			actions: []string{
				"pull",
			},
			want: Scope{ResourceType: "repository", ResourceName: "foo", Actions: []string{"pull"}},
		},
		{
			name:       "multiple actions",
			repository: "foo",
			actions: []string{
				"pull",
				"push",
			},
			want: Scope{ResourceType: "repository", ResourceName: "foo", Actions: []string{"pull", "push"}},
		},
		{
			name:       "unordered actions",
			repository: "foo",
			actions: []string{
				"push",
				"pull",
			},
			want: Scope{ResourceType: "repository", ResourceName: "foo", Actions: []string{"pull", "push"}},
		},
		{
			name:       "duplicated actions",
			repository: "foo",
			actions: []string{
				"push",
				"pull",
				"pull",
				"delete",
				"push",
			},
			want: Scope{ResourceType: "repository", ResourceName: "foo", Actions: []string{"delete", "pull", "push"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ScopeRepository(tt.repository, tt.actions...); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ScopeRepository() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestScopeRepository_noActionAliasing ensures the caller's action slice is not
// reordered underneath it, as cleanActions sorts in place.
func TestScopeRepository_noActionAliasing(t *testing.T) {
	actions := []string{ActionPush, ActionPull}
	ScopeRepository("foo", actions...)
	if want := []string{ActionPush, ActionPull}; !reflect.DeepEqual(actions, want) {
		t.Errorf("ScopeRepository() reordered the caller's actions to %v, want %v", actions, want)
	}
}

func TestScopeRegistryCatalog(t *testing.T) {
	want := Scope{ResourceType: "registry", ResourceName: "catalog", Actions: []string{"*"}}
	got := ScopeRegistryCatalog()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ScopeRegistryCatalog() = %v, want %v", got, want)
	}
	// mutating the returned scope must not affect a later call
	got.Actions[0] = ActionPull
	if again := ScopeRegistryCatalog(); !reflect.DeepEqual(again, want) {
		t.Errorf("ScopeRegistryCatalog() = %v after the previous result was mutated, want %v", again, want)
	}
}

func TestAppendRepositoryScope(t *testing.T) {
	ctx := context.Background()
	ref1, err := properties.NewReference("registry.example.com/foo")
	if err != nil {
		t.Fatal("properties.NewReference() error =", err)
	}
	ref2, err := properties.NewReference("docker.io/foo")
	if err != nil {
		t.Fatal("properties.NewReference() error =", err)
	}

	// with single scope
	want1 := []Scope{
		ScopeRepository("foo", ActionPull),
	}
	want2 := []Scope{
		ScopeRepository("foo", ActionPush),
	}
	ctx = AppendRepositoryScope(ctx, ref1, ActionPull)
	ctx = AppendRepositoryScope(ctx, ref2, ActionPush)
	if got := GetScopesForResource(ctx, ref1.Resource()); !reflect.DeepEqual(got, want1) {
		t.Errorf("GetScopesForResource(AppendRepositoryScope()) = %v, want %v", got, want1)
	}
	if got := GetScopesForResource(ctx, ref2.Resource()); !reflect.DeepEqual(got, want2) {
		t.Errorf("GetScopesForResource(AppendRepositoryScope()) = %v, want %v", got, want2)
	}

	// with duplicated scopes
	actions1 := []string{
		ActionDelete,
		ActionDelete,
		ActionPull,
	}
	want1 = []Scope{
		ScopeRepository("foo", ActionDelete, ActionPull),
	}
	actions2 := []string{
		ActionPush,
		ActionPush,
		ActionDelete,
	}
	want2 = []Scope{
		ScopeRepository("foo", ActionDelete, ActionPush),
	}
	ctx = AppendRepositoryScope(ctx, ref1, actions1...)
	ctx = AppendRepositoryScope(ctx, ref2, actions2...)
	if got := GetScopesForResource(ctx, ref1.Resource()); !reflect.DeepEqual(got, want1) {
		t.Errorf("GetScopesForResource(AppendRepositoryScope()) = %v, want %v", got, want1)
	}
	if got := GetScopesForResource(ctx, ref2.Resource()); !reflect.DeepEqual(got, want2) {
		t.Errorf("GetScopesForResource(AppendRepositoryScope()) = %v, want %v", got, want2)
	}

	// append empty scopes
	ctx = AppendRepositoryScope(ctx, ref1)
	ctx = AppendRepositoryScope(ctx, ref2)
	if got := GetScopesForResource(ctx, ref1.Resource()); !reflect.DeepEqual(got, want1) {
		t.Errorf("GetScopesForResource(AppendRepositoryScope()) = %v, want %v", got, want1)
	}
	if got := GetScopesForResource(ctx, ref2.Resource()); !reflect.DeepEqual(got, want2) {
		t.Errorf("GetScopesForResource(AppendRepositoryScope()) = %v, want %v", got, want2)
	}
}

// TestAppendRepositoryScope_namespaceIsolation is the case #1376 is about: two
// namespaces of one registry threaded through a single context must not disclose
// each other's repositories in a token request.
func TestAppendRepositoryScope_namespaceIsolation(t *testing.T) {
	ctx := context.Background()
	ref1, err := properties.NewReference("harbor.local/namespace1/app")
	if err != nil {
		t.Fatal("properties.NewReference() error =", err)
	}
	ref2, err := properties.NewReference("harbor.local/namespace2/app")
	if err != nil {
		t.Fatal("properties.NewReference() error =", err)
	}
	ctx = AppendRepositoryScope(ctx, ref1, ActionPull, ActionPush)
	ctx = AppendRepositoryScope(ctx, ref2, ActionPull, ActionPush)

	want1 := []Scope{ScopeRepository("namespace1/app", ActionPull, ActionPush)}
	if got := GetScopesForResource(ctx, ref1.Resource()); !reflect.DeepEqual(got, want1) {
		t.Errorf("GetScopesForResource(namespace1/app) = %v, want %v", got, want1)
	}
	want2 := []Scope{ScopeRepository("namespace2/app", ActionPull, ActionPush)}
	if got := GetScopesForResource(ctx, ref2.Resource()); !reflect.DeepEqual(got, want2) {
		t.Errorf("GetScopesForResource(namespace2/app) = %v, want %v", got, want2)
	}
}

func TestWithScopesForResource(t *testing.T) {
	ctx := context.Background()
	res1 := properties.Resource{Registry: "registry1.example.com"}
	res2 := properties.Resource{Registry: "registry2.example.com"}

	// with single scope
	want1 := []Scope{ScopeRepository("foo", ActionPull)}
	want2 := []Scope{ScopeRepository("foo", ActionPush)}
	ctx = WithScopesForResource(ctx, res1, want1...)
	ctx = WithScopesForResource(ctx, res2, want2...)
	if got := GetScopesForResource(ctx, res1); !reflect.DeepEqual(got, want1) {
		t.Errorf("GetScopesForResource(WithScopesForResource()) = %v, want %v", got, want1)
	}
	if got := GetScopesForResource(ctx, res2); !reflect.DeepEqual(got, want2) {
		t.Errorf("GetScopesForResource(WithScopesForResource()) = %v, want %v", got, want2)
	}
	unrelated := properties.Resource{Registry: "registry3.example.com"}
	if got := GetScopesForResource(ctx, unrelated); got != nil {
		t.Errorf("GetScopesForResource() = %v for an unrelated registry, want nil", got)
	}

	// overwrite scopes
	want1 = []Scope{ScopeRepository("bar", ActionPush)}
	want2 = []Scope{ScopeRepository("bar", ActionPull)}
	ctx = WithScopesForResource(ctx, res1, want1...)
	ctx = WithScopesForResource(ctx, res2, want2...)
	if got := GetScopesForResource(ctx, res1); !reflect.DeepEqual(got, want1) {
		t.Errorf("GetScopesForResource(WithScopesForResource()) = %v, want %v", got, want1)
	}
	if got := GetScopesForResource(ctx, res2); !reflect.DeepEqual(got, want2) {
		t.Errorf("GetScopesForResource(WithScopesForResource()) = %v, want %v", got, want2)
	}

	// overwrite scopes with de-duplication
	scopes1 := []Scope{
		ScopeRepository("hello-world", ActionPush),
		ScopeRepository("alpine", ActionDelete),
		ScopeRepository("hello-world", ActionPull),
		ScopeRepository("alpine", ActionDelete),
	}
	want1 = []Scope{
		ScopeRepository("alpine", ActionDelete),
		ScopeRepository("hello-world", ActionPull, ActionPush),
	}
	ctx = WithScopesForResource(ctx, res1, scopes1...)
	if got := GetScopesForResource(ctx, res1); !reflect.DeepEqual(got, want1) {
		t.Errorf("GetScopesForResource(WithScopesForResource()) = %v, want %v", got, want1)
	}

	// clean scopes
	ctx = WithScopesForResource(ctx, res1)
	if got := GetScopesForResource(ctx, res1); got != nil {
		t.Errorf("GetScopesForResource(WithScopesForResource()) = %v, want nil", got)
	}
	// removing the hints of one resource leaves the others alone
	if got := GetScopesForResource(ctx, res2); !reflect.DeepEqual(got, want2) {
		t.Errorf("GetScopesForResource() = %v after another resource was cleared, want %v", got, want2)
	}
}

// TestWithScopesForResource_copyOnWrite ensures a derived context does not
// change what its parent sees.
func TestWithScopesForResource_copyOnWrite(t *testing.T) {
	res := properties.Resource{Registry: "registry.example.com"}
	narrow := properties.Resource{Registry: "registry.example.com", Path: "foo"}
	want := []Scope{ScopeRepository("foo", ActionPull)}

	parent := WithScopesForResource(context.Background(), res, want...)
	_ = WithScopesForResource(parent, narrow, ScopeRepository("foo", ActionPush))
	_ = WithScopesForResource(parent, res, ScopeRepository("bar", ActionPush))
	if got := GetScopesForResource(parent, res); !reflect.DeepEqual(got, want) {
		t.Errorf("GetScopesForResource(parent) = %v after deriving children, want %v", got, want)
	}
}

func TestAppendScopesForResource(t *testing.T) {
	ctx := context.Background()
	res1 := properties.Resource{Registry: "registry1.example.com"}
	res2 := properties.Resource{Registry: "registry2.example.com"}

	// with single scope
	want1 := []Scope{ScopeRepository("foo", ActionPull)}
	want2 := []Scope{ScopeRepository("foo", ActionPush)}
	ctx = AppendScopesForResource(ctx, res1, want1...)
	ctx = AppendScopesForResource(ctx, res2, want2...)
	if got := GetScopesForResource(ctx, res1); !reflect.DeepEqual(got, want1) {
		t.Errorf("GetScopesForResource(AppendScopesForResource()) = %v, want %v", got, want1)
	}
	if got := GetScopesForResource(ctx, res2); !reflect.DeepEqual(got, want2) {
		t.Errorf("GetScopesForResource(AppendScopesForResource()) = %v, want %v", got, want2)
	}

	// append scopes with de-duplication
	scopes1 := []Scope{
		ScopeRepository("hello-world", ActionPush),
		ScopeRepository("alpine", ActionDelete),
		ScopeRepository("hello-world", ActionPull),
		ScopeRepository("alpine", ActionDelete),
	}
	want1 = []Scope{
		ScopeRepository("alpine", ActionDelete),
		ScopeRepository("foo", ActionPull),
		ScopeRepository("hello-world", ActionPull, ActionPush),
	}
	ctx = AppendScopesForResource(ctx, res1, scopes1...)
	if got := GetScopesForResource(ctx, res1); !reflect.DeepEqual(got, want1) {
		t.Errorf("GetScopesForResource(AppendScopesForResource()) = %v, want %v", got, want1)
	}

	// append empty scopes
	ctx = AppendScopesForResource(ctx, res1)
	if got := GetScopesForResource(ctx, res1); !reflect.DeepEqual(got, want1) {
		t.Errorf("GetScopesForResource(AppendScopesForResource()) = %v, want %v", got, want1)
	}
}

// TestAppendScopesForResource_exactBucket checks that appending to a narrow
// resource does not copy the inherited hints down into it, which would re-grow
// the narrow bucket on every append.
func TestAppendScopesForResource_exactBucket(t *testing.T) {
	registry := properties.Resource{Registry: "registry.example.com"}
	repository := properties.Resource{Registry: "registry.example.com", Path: "ns/app"}

	ctx := WithScopesForResource(context.Background(), registry, ScopeRegistryCatalog())
	ctx = AppendScopesForResource(ctx, repository, ScopeRepository("ns/app", ActionPull))
	// the registry hints still apply, but only once
	want := []Scope{
		ScopeRegistryCatalog(),
		ScopeRepository("ns/app", ActionPull),
	}
	if got := GetScopesForResource(ctx, repository); !reflect.DeepEqual(got, want) {
		t.Errorf("GetScopesForResource() = %v, want %v", got, want)
	}
	// clearing the registry hints drops them from the narrow lookup too
	ctx = WithScopesForResource(ctx, registry)
	want = []Scope{ScopeRepository("ns/app", ActionPull)}
	if got := GetScopesForResource(ctx, repository); !reflect.DeepEqual(got, want) {
		t.Errorf("GetScopesForResource() = %v after clearing the registry hints, want %v", got, want)
	}
}

func TestGetScopesForResource_hierarchy(t *testing.T) {
	const registry = "harbor.local"
	ctx := context.Background()
	ctx = WithScopesForResource(ctx, properties.Resource{Registry: registry}, ScopeRegistryCatalog())
	ctx = WithScopesForResource(ctx, properties.Resource{Registry: registry, Path: "ns"},
		ScopeRepository("ns/shared", ActionPull))
	ctx = WithScopesForResource(ctx, properties.Resource{Registry: registry, Path: "ns/app"},
		ScopeRepository("ns/app", ActionPull, ActionPush))
	ctx = WithScopesForResource(ctx, properties.Resource{Registry: registry, Path: "nsother"},
		ScopeRepository("nsother/app", ActionPull))

	tests := []struct {
		name string
		path string
		want []Scope
	}{
		{
			name: "registry only sees the registry hints",
			want: []Scope{ScopeRegistryCatalog()},
		},
		{
			name: "namespace inherits from the registry",
			path: "ns",
			want: []Scope{
				ScopeRegistryCatalog(),
				ScopeRepository("ns/shared", ActionPull),
			},
		},
		{
			name: "repository inherits from its namespace and the registry",
			path: "ns/app",
			want: []Scope{
				ScopeRegistryCatalog(),
				ScopeRepository("ns/app", ActionPull, ActionPush),
				ScopeRepository("ns/shared", ActionPull),
			},
		},
		{
			name: "a sibling namespace does not leak",
			path: "nsother/app",
			want: []Scope{
				ScopeRegistryCatalog(),
				ScopeRepository("nsother/app", ActionPull),
			},
		},
		{
			name: "a prefix that is not a path segment does not match",
			path: "nsopqr",
			want: []Scope{ScopeRegistryCatalog()},
		},
		{
			name: "an unrelated repository only inherits from the registry",
			path: "other/app",
			want: []Scope{ScopeRegistryCatalog()},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resource := properties.Resource{Registry: registry, Path: tt.path}
			if got := GetScopesForResource(ctx, resource); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GetScopesForResource(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

// TestGetScopesForResource_canonicalRegistry pins the canonicalization of the
// registry name on both sides of the lookup. A regression here fails open: every
// lookup misses, the hints silently stop working, and nothing errors.
func TestGetScopesForResource_canonicalRegistry(t *testing.T) {
	want := []Scope{ScopeRepository("foo", ActionPull)}
	ctx := WithScopesForResource(context.Background(),
		properties.Resource{Registry: "docker.io", Path: "foo"}, want...)

	for _, registry := range []string{"docker.io", "registry-1.docker.io", "Registry-1.Docker.IO", "DOCKER.IO"} {
		resource := properties.Resource{Registry: registry, Path: "foo"}
		if got := GetScopesForResource(ctx, resource); !reflect.DeepEqual(got, want) {
			t.Errorf("GetScopesForResource(%q) = %v, want %v", registry, got, want)
		}
	}
}

// TestGetScopesForResource_noAliasing covers decision 2 of #1451: the actions of
// a returned scope must not be the slice held in the context, or a caller
// mutating them would corrupt every later request on that context.
func TestGetScopesForResource_noAliasing(t *testing.T) {
	resource := properties.Resource{Registry: "registry.example.com", Path: "foo"}
	want := []Scope{ScopeRepository("foo", ActionPull)}
	ctx := WithScopesForResource(context.Background(), resource, want...)

	got := GetScopesForResource(ctx, resource)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("GetScopesForResource() = %v, want %v", got, want)
	}
	got[0].Actions[0] = "corrupted"
	if again := GetScopesForResource(ctx, resource); !reflect.DeepEqual(again, want) {
		t.Errorf("GetScopesForResource() = %v after the previous result was mutated, want %v", again, want)
	}
}

func TestCleanScopes(t *testing.T) {
	tests := []struct {
		name   string
		scopes []Scope
		want   []Scope
	}{
		{
			name: "nil scope",
		},
		{
			name:   "empty scope",
			scopes: []Scope{},
		},
		{
			name:   "zero scope is dropped",
			scopes: []Scope{{}},
		},
		{
			name:   "scope without a resource name is dropped",
			scopes: []Scope{{ResourceType: "repository", Actions: []string{ActionPull}}},
		},
		{
			name:   "scope without a resource type is dropped",
			scopes: []Scope{{ResourceName: "foo", Actions: []string{ActionPull}}},
		},
		{
			name:   "scope without actions is dropped",
			scopes: []Scope{{ResourceType: "repository", ResourceName: "foo"}},
		},
		{
			name:   "scope with only empty actions is dropped",
			scopes: []Scope{{ResourceType: "repository", ResourceName: "foo", Actions: []string{"", ""}}},
		},
		{
			name:   "opaque scope is kept",
			scopes: []Scope{{ResourceType: "aws"}},
			want:   []Scope{{ResourceType: "aws"}},
		},
		{
			name: "opaque scope is de-duplicated and does not merge with a named one",
			scopes: []Scope{
				{ResourceType: "aws"},
				{ResourceType: "aws"},
				{ResourceType: "aws", ResourceName: "x", Actions: []string{ActionPull}},
			},
			want: []Scope{
				{ResourceType: "aws"},
				{ResourceType: "aws", ResourceName: "x", Actions: []string{ActionPull}},
			},
		},
		{
			name:   "single scope",
			scopes: []Scope{ScopeRepository("foo", ActionPull)},
			want:   []Scope{ScopeRepository("foo", ActionPull)},
		},
		{
			name: "single scope with unordered actions",
			scopes: []Scope{
				{ResourceType: "repository", ResourceName: "foo", Actions: []string{"push", "pull", "delete"}},
			},
			want: []Scope{ScopeRepository("foo", ActionDelete, ActionPull, ActionPush)},
		},
		{
			name: "single scope with duplicated actions",
			scopes: []Scope{
				{ResourceType: "repository", ResourceName: "foo", Actions: []string{"push", "pull", "push", "pull"}},
			},
			want: []Scope{ScopeRepository("foo", ActionPull, ActionPush)},
		},
		{
			name: "wildcard absorbs the other actions of the same resource",
			scopes: []Scope{
				ScopeRepository("foo", ActionPull),
				{ResourceType: "repository", ResourceName: "foo", Actions: []string{"*"}},
				ScopeRepository("foo", ActionPush),
			},
			want: []Scope{{ResourceType: "repository", ResourceName: "foo", Actions: []string{"*"}}},
		},
		{
			name: "multiple scopes",
			scopes: []Scope{
				ScopeRepository("bar", ActionPush),
				ScopeRepository("foo", ActionPull),
			},
			want: []Scope{
				ScopeRepository("bar", ActionPush),
				ScopeRepository("foo", ActionPull),
			},
		},
		{
			name: "multiple unordered scopes",
			scopes: []Scope{
				ScopeRepository("foo", ActionPull),
				ScopeRepository("bar", ActionPush),
			},
			want: []Scope{
				ScopeRepository("bar", ActionPush),
				ScopeRepository("foo", ActionPull),
			},
		},
		{
			name: "multiple scopes with duplicates",
			scopes: []Scope{
				ScopeRepository("foo", ActionPull),
				ScopeRepository("bar", ActionPush),
				ScopeRepository("foo", ActionPush),
				ScopeRepository("bar", ActionPush, ActionDelete, ActionPull),
				ScopeRepository("bar", ActionDelete, ActionPull),
				ScopeRepository("foo", ActionPull),
				ScopeRegistryCatalog(),
				{ResourceType: "registry", ResourceName: "catalog", Actions: []string{ActionPull}},
			},
			want: []Scope{
				ScopeRegistryCatalog(),
				ScopeRepository("bar", ActionDelete, ActionPull, ActionPush),
				ScopeRepository("foo", ActionPull, ActionPush),
			},
		},
		{
			// Sorting on the (type, name) tuple rather than on the serialized
			// string: ':' is 0x3a and '-' is 0x2d, so "foo-bar" sorts before
			// "foo" as a string and after it as a name.
			name: "sorted on the resource tuple, not on the wire form",
			scopes: []Scope{
				ScopeRepository("foo-bar", ActionPull),
				ScopeRepository("foo", ActionPull),
			},
			want: []Scope{
				ScopeRepository("foo", ActionPull),
				ScopeRepository("foo-bar", ActionPull),
			},
		},
		{
			name: "sorted on the resource type first",
			scopes: []Scope{
				ScopeRepository("aaa", ActionPull),
				ScopeRegistryCatalog(),
			},
			want: []Scope{
				ScopeRegistryCatalog(),
				ScopeRepository("aaa", ActionPull),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CleanScopes(tt.scopes); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CleanScopes() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestCleanScopes_deduplicatesEveryScope is the bug reported in #1451: the
// string implementation let a scope it could not parse through twice, so two
// semantically identical contexts produced different bearer cache keys. A typed
// scope has no unparseable bucket to fall into, so de-duplication is uniform.
func TestCleanScopes_deduplicatesEveryScope(t *testing.T) {
	tests := []struct {
		name   string
		scopes []Scope
		want   []Scope
	}{
		{
			name: "one scope",
			scopes: []Scope{
				{ResourceType: "repository", ResourceName: "foo", Actions: []string{ActionPull}},
			},
			want: []Scope{ScopeRepository("foo", ActionPull)},
		},
		{
			name: "the same scope twice",
			scopes: []Scope{
				{ResourceType: "repository", ResourceName: "foo", Actions: []string{ActionPull}},
				{ResourceType: "repository", ResourceName: "foo", Actions: []string{ActionPull}},
			},
			want: []Scope{ScopeRepository("foo", ActionPull)},
		},
		{
			name: "the same scope twice alongside another",
			scopes: []Scope{
				{ResourceType: "repository", ResourceName: "foo", Actions: []string{ActionPull}},
				{ResourceType: "repository", ResourceName: "foo", Actions: []string{ActionPull}},
				ScopeRepository("bar", ActionPull),
			},
			want: []Scope{
				ScopeRepository("bar", ActionPull),
				ScopeRepository("foo", ActionPull),
			},
		},
		{
			name: "the registry catalog scope twice",
			scopes: []Scope{
				ScopeRegistryCatalog(),
				ScopeRegistryCatalog(),
			},
			want: []Scope{ScopeRegistryCatalog()},
		},
		{
			// in=["badscope" "badscope"] -> out=["badscope" "badscope"] before
			name: "an opaque scope twice",
			scopes: []Scope{
				{ResourceType: "badscope"},
				{ResourceType: "badscope"},
			},
			want: []Scope{{ResourceType: "badscope"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CleanScopes(tt.scopes)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CleanScopes() = %v, want %v", got, tt.want)
			}
			// the same scopes in the reverse order yield the same cache key
			reversed := make([]Scope, len(tt.scopes))
			for i, scope := range tt.scopes {
				reversed[len(tt.scopes)-1-i] = scope
			}
			if want, got := joinScopes(got), joinScopes(CleanScopes(reversed)); got != want {
				t.Errorf("cache key of the reversed scopes = %q, want %q", got, want)
			}
		})
	}
}

// TestCleanScopes_noAliasing covers decision 2 of #1451: CleanScopes must never
// retain or reorder a caller-supplied Actions slice.
func TestCleanScopes_noAliasing(t *testing.T) {
	actions := []string{ActionPush, ActionPull}
	scopes := []Scope{{ResourceType: "repository", ResourceName: "foo", Actions: actions}}

	cleaned := CleanScopes(scopes)
	if want := []string{ActionPush, ActionPull}; !reflect.DeepEqual(actions, want) {
		t.Errorf("CleanScopes() reordered the caller's actions to %v, want %v", actions, want)
	}
	cleaned[0].Actions[0] = "corrupted"
	if want := []string{ActionPush, ActionPull}; !reflect.DeepEqual(actions, want) {
		t.Errorf("CleanScopes() aliased the caller's actions, now %v, want %v", actions, want)
	}
}

func Test_scopeHintApplies(t *testing.T) {
	tests := []struct {
		hintPath string
		reqPath  string
		want     bool
	}{
		{hintPath: "", reqPath: "", want: true},
		{hintPath: "", reqPath: "ns/app", want: true},
		{hintPath: "ns", reqPath: "ns", want: true},
		{hintPath: "ns", reqPath: "ns/app", want: true},
		{hintPath: "ns", reqPath: "ns/sub/app", want: true},
		{hintPath: "ns", reqPath: "nsother", want: false},
		{hintPath: "ns", reqPath: "nsother/app", want: false},
		{hintPath: "ns/app", reqPath: "ns", want: false},
		{hintPath: "ns/app", reqPath: "", want: false},
		{hintPath: "ns/app", reqPath: "ns/application", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.hintPath+"|"+tt.reqPath, func(t *testing.T) {
			if got := scopeHintApplies(tt.hintPath, tt.reqPath); got != tt.want {
				t.Errorf("scopeHintApplies(%q, %q) = %v, want %v", tt.hintPath, tt.reqPath, got, tt.want)
			}
		})
	}
}

func Test_joinScopes(t *testing.T) {
	tests := []struct {
		name   string
		scopes []Scope
		want   string
	}{
		{
			name: "nil scopes",
		},
		{
			name:   "a scope with no wire form is omitted",
			scopes: []Scope{{}, ScopeRepository("foo", ActionPull), {ResourceName: "bar", Actions: []string{ActionPull}}},
			want:   "repository:foo:pull",
		},
		{
			name: "multiple scopes are space-separated",
			scopes: []Scope{
				ScopeRegistryCatalog(),
				ScopeRepository("foo", ActionPull, ActionPush),
			},
			want: "registry:catalog:* repository:foo:pull,push",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := joinScopes(tt.scopes); got != tt.want {
				t.Errorf("joinScopes() = %q, want %q", got, tt.want)
			}
		})
	}
}

func Test_cleanActions(t *testing.T) {
	tests := []struct {
		name    string
		actions []string
		want    []string
	}{
		{
			name: "nil action",
		},
		{
			name:    "empty action",
			actions: []string{},
		},
		{
			name: "single action",
			actions: []string{
				"pull",
			},
			want: []string{
				"pull",
			},
		},
		{
			name: "single empty action",
			actions: []string{
				"",
			},
		},
		{
			name: "multiple actions",
			actions: []string{
				"pull",
				"push",
			},
			want: []string{
				"pull",
				"push",
			},
		},
		{
			name: "multiple actions with empty action",
			actions: []string{
				"pull",
				"",
				"push",
			},
			want: []string{
				"pull",
				"push",
			},
		},
		{
			name: "multiple actions with all empty action",
			actions: []string{
				"",
				"",
				"",
			},
			want: nil,
		},
		{
			name: "unordered actions",
			actions: []string{
				"push",
				"pull",
				"delete",
			},
			want: []string{
				"delete",
				"pull",
				"push",
			},
		},
		{
			name: "wildcard",
			actions: []string{
				"*",
			},
			want: []string{
				"*",
			},
		},
		{
			name: "wildcard at the begining",
			actions: []string{
				"*",
				"push",
				"pull",
				"delete",
			},
			want: []string{
				"*",
			},
		},
		{
			name: "wildcard in the middle",
			actions: []string{
				"push",
				"pull",
				"*",
				"delete",
			},
			want: []string{
				"*",
			},
		},
		{
			name: "wildcard at the end",
			actions: []string{
				"push",
				"pull",
				"delete",
				"*",
			},
			want: []string{
				"*",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cleanActions(tt.actions); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("cleanActions() = %v, want %v", got, tt.want)
			}
		})
	}
}
