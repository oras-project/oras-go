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
	"reflect"
	"testing"

	"oras.land/oras-go/v2/registry"
)

func TestScopeRepository(t *testing.T) {
	tests := []struct {
		name       string
		repository string
		actions    []string
		want       string
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
			name:       "empty actions",
			repository: "foo",
			actions:    []string{},
		},
		{
			name:       "empty actions list",
			repository: "foo",
			actions:    []string{},
		},
		{
			name:       "empty actions",
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
			want: "repository:foo:pull",
		},
		{
			name:       "multiple actions",
			repository: "foo",
			actions: []string{
				"pull",
				"push",
			},
			want: "repository:foo:pull,push",
		},
		{
			name:       "unordered actions",
			repository: "foo",
			actions: []string{
				"push",
				"pull",
			},
			want: "repository:foo:pull,push",
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
			want: "repository:foo:delete,pull,push",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ScopeRepository(tt.repository, tt.actions...); got != tt.want {
				t.Errorf("ScopeRepository() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWithScopeHints(t *testing.T) {
	ctx := context.Background()
	ref1, err := registry.ParseReference("registry.example.com/foo")
	if err != nil {
		t.Fatal("registry.ParseReference() error =", err)
	}
	ref2, err := registry.ParseReference("docker.io/foo")
	if err != nil {
		t.Fatal("registry.ParseReference() error =", err)
	}

	// with single scope
	want1 := []string{
		"repository:foo:pull",
	}
	want2 := []string{
		"repository:foo:push",
	}
	ctx = AppendRepositoryScope(ctx, ref1, ActionPull)
	ctx = AppendRepositoryScope(ctx, ref2, ActionPush)
	if got := GetScopesForHost(ctx, ref1.Host()); !reflect.DeepEqual(got, want1) {
		t.Errorf("GetScopesPerRegistry(WithScopeHints()) = %v, want %v", got, want1)
	}
	if got := GetScopesForHost(ctx, ref2.Host()); !reflect.DeepEqual(got, want2) {
		t.Errorf("GetScopesPerRegistry(WithScopeHints()) = %v, want %v", got, want2)
	}

	// with duplicated scopes
	scopes1 := []string{
		ActionDelete,
		ActionDelete,
		ActionPull,
	}
	want1 = []string{
		"repository:foo:delete,pull",
	}
	scopes2 := []string{
		ActionPush,
		ActionPush,
		ActionDelete,
	}
	want2 = []string{
		"repository:foo:delete,push",
	}
	ctx = AppendRepositoryScope(ctx, ref1, scopes1...)
	ctx = AppendRepositoryScope(ctx, ref2, scopes2...)
	if got := GetScopesForHost(ctx, ref1.Host()); !reflect.DeepEqual(got, want1) {
		t.Errorf("GetScopesPerRegistry(WithScopeHints()) = %v, want %v", got, want1)
	}
	if got := GetScopesForHost(ctx, ref2.Host()); !reflect.DeepEqual(got, want2) {
		t.Errorf("GetScopesPerRegistry(WithScopeHints()) = %v, want %v", got, want2)
	}

	// append empty scopes
	ctx = AppendRepositoryScope(ctx, ref1)
	ctx = AppendRepositoryScope(ctx, ref2)
	if got := GetScopesForHost(ctx, ref1.Host()); !reflect.DeepEqual(got, want1) {
		t.Errorf("GetScopesPerRegistry(WithScopeHints()) = %v, want %v", got, want1)
	}
	if got := GetScopesForHost(ctx, ref2.Host()); !reflect.DeepEqual(got, want2) {
		t.Errorf("GetScopesPerRegistry(WithScopeHints()) = %v, want %v", got, want2)
	}
}

func TestWithScopes(t *testing.T) {
	ctx := context.Background()

	// with single scope
	want := []string{
		"repository:foo:pull",
	}
	ctx = WithScopes(ctx, want...)
	if got := GetScopes(ctx); !reflect.DeepEqual(got, want) {
		t.Errorf("GetScopes(WithScopes()) = %v, want %v", got, want)
	}

	// overwrite scopes
	want = []string{
		"repository:bar:push",
	}
	ctx = WithScopes(ctx, want...)
	if got := GetScopes(ctx); !reflect.DeepEqual(got, want) {
		t.Errorf("GetScopes(WithScopes()) = %v, want %v", got, want)
	}

	// overwrite scopes with de-duplication
	scopes := []string{
		"repository:hello-world:push",
		"repository:alpine:delete",
		"repository:hello-world:pull",
		"repository:alpine:delete",
	}
	want = []string{
		"repository:alpine:delete",
		"repository:hello-world:pull,push",
	}
	ctx = WithScopes(ctx, scopes...)
	if got := GetScopes(ctx); !reflect.DeepEqual(got, want) {
		t.Errorf("GetScopes(WithScopes()) = %v, want %v", got, want)
	}

	// clean scopes
	want = nil
	ctx = WithScopes(ctx, want...)
	if got := GetScopes(ctx); !reflect.DeepEqual(got, want) {
		t.Errorf("GetScopes(WithScopes()) = %v, want %v", got, want)
	}
}

func TestAppendScopes(t *testing.T) {
	ctx := context.Background()

	// append single scope
	want := []string{
		"repository:foo:pull",
	}
	ctx = AppendScopes(ctx, want...)
	if got := GetScopes(ctx); !reflect.DeepEqual(got, want) {
		t.Errorf("GetScopes(AppendScopes()) = %v, want %v", got, want)
	}

	// append scopes with de-duplication
	scopes := []string{
		"repository:hello-world:push",
		"repository:alpine:delete",
		"repository:hello-world:pull",
		"repository:alpine:delete",
	}
	want = []string{
		"repository:alpine:delete",
		"repository:foo:pull",
		"repository:hello-world:pull,push",
	}
	ctx = AppendScopes(ctx, scopes...)
	if got := GetScopes(ctx); !reflect.DeepEqual(got, want) {
		t.Errorf("GetScopes(AppendScopes()) = %v, want %v", got, want)
	}

	// append empty scopes
	ctx = AppendScopes(ctx)
	if got := GetScopes(ctx); !reflect.DeepEqual(got, want) {
		t.Errorf("GetScopes(AppendScopes()) = %v, want %v", got, want)
	}
}

func TestWithScopesPerHost(t *testing.T) {
	ctx := context.Background()
	reg1 := "registry1.example.com"
	reg2 := "registry2.example.com"

	// with single scope
	want1 := []string{
		"repository:foo:pull",
	}
	want2 := []string{
		"repository:foo:push",
	}
	ctx = WithScopesForHost(ctx, reg1, want1...)
	ctx = WithScopesForHost(ctx, reg2, want2...)
	if got := GetScopesForHost(ctx, reg1); !reflect.DeepEqual(got, want1) {
		t.Errorf("GetScopesPerRegistry(WithScopesPerRegistry()) = %v, want %v", got, want1)
	}
	if got := GetScopesForHost(ctx, reg2); !reflect.DeepEqual(got, want2) {
		t.Errorf("GetScopesPerRegistry(WithScopesPerRegistry()) = %v, want %v", got, want2)
	}

	// overwrite scopes
	want1 = []string{
		"repository:bar:push",
	}
	want2 = []string{
		"repository:bar:pull",
	}
	ctx = WithScopesForHost(ctx, reg1, want1...)
	ctx = WithScopesForHost(ctx, reg2, want2...)
	if got := GetScopesForHost(ctx, reg1); !reflect.DeepEqual(got, want1) {
		t.Errorf("GetScopesPerRegistry(WithScopesPerRegistry()) = %v, want %v", got, want1)
	}
	if got := GetScopesForHost(ctx, reg2); !reflect.DeepEqual(got, want2) {
		t.Errorf("GetScopesPerRegistry(WithScopesPerRegistry()) = %v, want %v", got, want2)
	}

	// overwrite scopes with de-duplication
	scopes1 := []string{
		"repository:hello-world:push",
		"repository:alpine:delete",
		"repository:hello-world:pull",
		"repository:alpine:delete",
	}
	want1 = []string{
		"repository:alpine:delete",
		"repository:hello-world:pull,push",
	}
	scopes2 := []string{
		"repository:goodbye-world:push",
		"repository:nginx:delete",
		"repository:goodbye-world:pull",
		"repository:nginx:delete",
	}
	want2 = []string{
		"repository:goodbye-world:pull,push",
		"repository:nginx:delete",
	}
	ctx = WithScopesForHost(ctx, reg1, scopes1...)
	ctx = WithScopesForHost(ctx, reg2, scopes2...)
	if got := GetScopesForHost(ctx, reg1); !reflect.DeepEqual(got, want1) {
		t.Errorf("GetScopesPerRegistry(WithScopesPerRegistry()) = %v, want %v", got, want1)
	}
	if got := GetScopesForHost(ctx, reg2); !reflect.DeepEqual(got, want2) {
		t.Errorf("GetScopesPerRegistry(WithScopesPerRegistry()) = %v, want %v", got, want2)
	}

	// clean scopes
	var want []string
	ctx = WithScopesForHost(ctx, reg1, want...)
	ctx = WithScopesForHost(ctx, reg2, want...)
	if got := GetScopesForHost(ctx, reg1); !reflect.DeepEqual(got, want) {
		t.Errorf("GetScopesPerRegistry(WithScopesPerRegistry()) = %v, want %v", got, want)
	}
	if got := GetScopesForHost(ctx, reg2); !reflect.DeepEqual(got, want) {
		t.Errorf("GetScopesPerRegistry(WithScopesPerRegistry()) = %v, want %v", got, want)
	}
}

func TestAppendScopesPerHost(t *testing.T) {
	ctx := context.Background()
	reg1 := "registry1.example.com"
	reg2 := "registry2.example.com"

	// with single scope
	want1 := []string{
		"repository:foo:pull",
	}
	want2 := []string{
		"repository:foo:push",
	}
	ctx = AppendScopesForHost(ctx, reg1, want1...)
	ctx = AppendScopesForHost(ctx, reg2, want2...)
	if got := GetScopesForHost(ctx, reg1); !reflect.DeepEqual(got, want1) {
		t.Errorf("GetScopesPerRegistry(AppendScopesPerRegistry()) = %v, want %v", got, want1)
	}
	if got := GetScopesForHost(ctx, reg2); !reflect.DeepEqual(got, want2) {
		t.Errorf("GetScopesPerRegistry(AppendScopesPerRegistry()) = %v, want %v", got, want2)
	}

	// append scopes with de-duplication
	scopes1 := []string{
		"repository:hello-world:push",
		"repository:alpine:delete",
		"repository:hello-world:pull",
		"repository:alpine:delete",
	}
	want1 = []string{
		"repository:alpine:delete",
		"repository:foo:pull",
		"repository:hello-world:pull,push",
	}
	scopes2 := []string{
		"repository:goodbye-world:push",
		"repository:nginx:delete",
		"repository:goodbye-world:pull",
		"repository:nginx:delete",
	}
	want2 = []string{
		"repository:foo:push",
		"repository:goodbye-world:pull,push",
		"repository:nginx:delete",
	}
	ctx = AppendScopesForHost(ctx, reg1, scopes1...)
	ctx = AppendScopesForHost(ctx, reg2, scopes2...)
	if got := GetScopesForHost(ctx, reg1); !reflect.DeepEqual(got, want1) {
		t.Errorf("GetScopesPerRegistry(AppendScopesPerRegistry()) = %v, want %v", got, want1)
	}
	if got := GetScopesForHost(ctx, reg2); !reflect.DeepEqual(got, want2) {
		t.Errorf("GetScopesPerRegistry(AppendScopesPerRegistry()) = %v, want %v", got, want2)
	}

	// append empty scopes
	ctx = AppendScopesForHost(ctx, reg1)
	ctx = AppendScopesForHost(ctx, reg2)
	if got := GetScopesForHost(ctx, reg1); !reflect.DeepEqual(got, want1) {
		t.Errorf("GetScopesPerRegistry(AppendScopesPerRegistry()) = %v, want %v", got, want1)
	}
	if got := GetScopesForHost(ctx, reg2); !reflect.DeepEqual(got, want2) {
		t.Errorf("GetScopesPerRegistry(AppendScopesPerRegistry()) = %v, want %v", got, want2)
	}
}

func TestCleanScopes(t *testing.T) {
	tests := []struct {
		name   string
		scopes []string
		want   []string
	}{
		{
			name: "nil scope",
		},
		{
			name:   "empty scope",
			scopes: []string{},
		},
		{
			name: "single scope",
			scopes: []string{
				"repository:foo:pull",
			},
			want: []string{
				"repository:foo:pull",
			},
		},
		{
			name: "single scope with unordered actions",
			scopes: []string{
				"repository:foo:push,pull,delete",
			},
			want: []string{
				"repository:foo:delete,pull,push",
			},
		},
		{
			name: "single scope with duplicated actions",
			scopes: []string{
				"repository:foo:push,pull,push,pull,push,push,pull",
			},
			want: []string{
				"repository:foo:pull,push",
			},
		},
		{
			name: "single scope with wild cards",
			scopes: []string{
				"repository:foo:pull,*,push",
			},
			want: []string{
				"repository:foo:*",
			},
		},
		{
			name: "single scope with no actions",
			scopes: []string{
				"repository:foo:,",
			},
			want: nil,
		},
		{
			name: "multiple scopes",
			scopes: []string{
				"repository:bar:push",
				"repository:foo:pull",
			},
			want: []string{
				"repository:bar:push",
				"repository:foo:pull",
			},
		},
		{
			name: "multiple unordered scopes",
			scopes: []string{
				"repository:foo:pull",
				"repository:bar:push",
			},
			want: []string{
				"repository:bar:push",
				"repository:foo:pull",
			},
		},
		{
			name: "multiple scopes with duplicates",
			scopes: []string{
				"repository:foo:pull",
				"repository:bar:push",
				"repository:foo:push",
				"repository:bar:push,delete,pull",
				"repository:bar:delete,pull",
				"repository:foo:pull",
				"registry:catalog:*",
				"registry:catalog:pull",
			},
			want: []string{
				"registry:catalog:*",
				"repository:bar:delete,pull,push",
				"repository:foo:pull,push",
			},
		},
		{
			name: "multiple scopes with no actions",
			scopes: []string{
				"repository:foo:,",
				"repository:bar:,",
			},
			want: nil,
		},
		{
			name: "single unknown or invalid scope",
			scopes: []string{
				"unknown",
			},
			want: []string{
				"unknown",
			},
		},
		{
			name: "multiple unknown or invalid scopes",
			scopes: []string{
				"repository:foo:pull",
				"unknown",
				"invalid:scope",
				"no:actions:",
				"repository:foo:push",
			},
			want: []string{
				"invalid:scope",
				"repository:foo:pull,push",
				"unknown",
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

func Test_getAllScopesForHost(t *testing.T) {
	host := "registry.example.com"
	tests := []struct {
		name         string
		scopes       []string
		globalScopes []string
		want         []string
	}{
		{
			name:   "Empty per-host scopes",
			scopes: []string{},
			globalScopes: []string{
				"repository:hello-world:push",
				"repository:alpine:delete",
				"repository:hello-world:pull",
				"repository:alpine:delete",
			},
			want: []string{
				"repository:alpine:delete",
				"repository:hello-world:pull,push",
			},
		},
		{
			name: "Empty global scopes",
			scopes: []string{
				"repository:hello-world:push",
				"repository:alpine:delete",
				"repository:hello-world:pull",
				"repository:alpine:delete",
			},
			globalScopes: []string{},
			want: []string{
				"repository:alpine:delete",
				"repository:hello-world:pull,push",
			},
		},
		{
			name: "Per-host scopes + global scopes",
			scopes: []string{
				"repository:hello-world:push",
				"repository:alpine:delete",
				"repository:hello-world:pull",
				"repository:alpine:delete",
			},
			globalScopes: []string{
				"repository:foo:pull",
				"repository:hello-world:pull",
				"repository:alpine:pull",
			},
			want: []string{
				"repository:alpine:delete,pull",
				"repository:foo:pull",
				"repository:hello-world:pull,push",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			ctx = WithScopesForHost(ctx, host, tt.scopes...)
			ctx = WithScopes(ctx, tt.globalScopes...)
			if got := GetAllScopesForHost(ctx, host); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("getAllScopesForHost() = %v, want %v", got, tt.want)
			}
		})
	}
}
