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

package config

import (
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/oras-project/oras-go/v3/registry/remote/auth"
)

func Test_resolveBlockedRealmPrefixes(t *testing.T) {
	defaults := auth.DefaultBlockedRealmPrefixes()
	private := auth.PrivateRealmPrefixes()

	tests := []struct {
		name    string
		entries []string
		want    []netip.Prefix
		wantErr string
	}{
		{
			name:    "empty input",
			entries: []string{},
			want:    []netip.Prefix{},
		},
		{
			name:    "none alone",
			entries: []string{"none"},
			want:    []netip.Prefix{},
		},
		{
			name:    "default",
			entries: []string{"default"},
			want:    defaults,
		},
		{
			name:    "private",
			entries: []string{"private"},
			want:    private,
		},
		{
			name:    "default and private",
			entries: []string{"default", "private"},
			want:    append(defaults, private...),
		},
		{
			name:    "CIDR",
			entries: []string{"10.0.0.0/8"},
			want:    []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")},
		},
		{
			name:    "bare IPv4",
			entries: []string{"192.0.2.1"},
			want:    []netip.Prefix{netip.PrefixFrom(netip.MustParseAddr("192.0.2.1"), 32)},
		},
		{
			name:    "bare IPv6",
			entries: []string{"2001:db8::1"},
			want:    []netip.Prefix{netip.PrefixFrom(netip.MustParseAddr("2001:db8::1"), 128)},
		},
		{
			name:    "mixed named and CIDR",
			entries: []string{"default", "10.0.0.0/8"},
			want:    append(defaults, netip.MustParsePrefix("10.0.0.0/8")),
		},
		{
			name:    "none with other entry",
			entries: []string{"none", "default"},
			wantErr: "blocked-realm-prefixes",
		},
		{
			name:    "unknown name",
			entries: []string{"strict"},
			wantErr: "blocked-realm-prefixes",
		},
		{
			name:    "hostname",
			entries: []string{"token.example.com"},
			wantErr: "blocked-realm-prefixes",
		},
		{
			name:    "bad CIDR",
			entries: []string{"10.0.0.0/33"},
			wantErr: "blocked-realm-prefixes",
		},
		{
			name:    "IPv4-mapped prefix",
			entries: []string{"::ffff:10.0.0.0/104"},
			wantErr: "IPv4-mapped prefixes in IPv4 form",
		},
		{
			name:    "bare IPv4-mapped address",
			entries: []string{"::ffff:10.0.0.1"},
			want:    []netip.Prefix{netip.PrefixFrom(netip.MustParseAddr("10.0.0.1"), 32)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveBlockedRealmPrefixes(tt.entries)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatal("resolveBlockedRealmPrefixes() error = nil, want an error")
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %v, want it to mention %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveBlockedRealmPrefixes() unexpected error: %v", err)
			}
			if got == nil {
				t.Fatal("resolveBlockedRealmPrefixes() = nil, want non-nil slice")
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("resolveBlockedRealmPrefixes() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLoadRegistriesConfig_BlockedRealmPrefixes_decode(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		wantNil   bool
		wantEmpty bool
		want      []string
	}{
		{
			name: "absent key",
			content: `
[[registry]]
prefix = "example.com"
`,
			wantNil: true,
		},
		{
			name: "empty array",
			content: `
[[registry]]
prefix = "example.com"
blocked-realm-prefixes = []
`,
			wantEmpty: true,
		},
		{
			name: "explicit list",
			content: `
[[registry]]
prefix = "example.com"
blocked-realm-prefixes = ["default", "private"]
`,
			want: []string{"default", "private"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "registries.conf")
			if err := os.WriteFile(path, []byte(tt.content), 0o600); err != nil {
				t.Fatalf("writing config: %v", err)
			}
			cfg, err := LoadRegistriesConfig(path)
			if err != nil {
				t.Fatalf("LoadRegistriesConfig() error: %v", err)
			}
			p := cfg.Registries[0].BlockedRealmPrefixes
			if tt.wantNil {
				if p != nil {
					t.Errorf("BlockedRealmPrefixes = %v, want nil", p)
				}
				return
			}
			if p == nil {
				t.Fatal("BlockedRealmPrefixes = nil, want non-nil")
			}
			if tt.wantEmpty {
				if len(p) != 0 {
					t.Errorf("BlockedRealmPrefixes = %v, want empty", p)
				}
				return
			}
			if !reflect.DeepEqual(p, tt.want) {
				t.Errorf("BlockedRealmPrefixes = %v, want %v", p, tt.want)
			}
		})
	}
}

func TestNewRegistryProperties_BlockedRealmPrefixes(t *testing.T) {
	defaults := auth.DefaultBlockedRealmPrefixes()
	private := auth.PrivateRealmPrefixes()

	tests := []struct {
		name       string
		prefixes   []string
		prefixesOK bool // false means leave field unset (nil)
		wantNil    bool
		wantEmpty  bool
		want       []netip.Prefix
		wantErr    string
	}{
		{
			name:       "absent leaves attributes nil",
			prefixesOK: false,
			wantNil:    true,
		},
		{
			name:       "empty array blocks nothing",
			prefixes:   []string{},
			prefixesOK: true,
			wantEmpty:  true,
		},
		{
			name:       "none blocks nothing",
			prefixes:   []string{"none"},
			prefixesOK: true,
			wantEmpty:  true,
		},
		{
			name:       "default expands",
			prefixes:   []string{"default"},
			prefixesOK: true,
			want:       defaults,
		},
		{
			name:       "private expands",
			prefixes:   []string{"private"},
			prefixesOK: true,
			want:       private,
		},
		{
			name:       "default and private",
			prefixes:   []string{"default", "private"},
			prefixesOK: true,
			want:       append(defaults, private...),
		},
		{
			name:       "mixed with CIDR",
			prefixes:   []string{"default", "10.0.0.0/8"},
			prefixesOK: true,
			want:       append(defaults, netip.MustParsePrefix("10.0.0.0/8")),
		},
		{
			name:       "bare IPv4",
			prefixes:   []string{"192.0.2.1"},
			prefixesOK: true,
			want:       []netip.Prefix{netip.PrefixFrom(netip.MustParseAddr("192.0.2.1"), 32)},
		},
		{
			name:       "bare IPv6",
			prefixes:   []string{"2001:db8::1"},
			prefixesOK: true,
			want:       []netip.Prefix{netip.PrefixFrom(netip.MustParseAddr("2001:db8::1"), 128)},
		},
		{
			name:       "unknown name",
			prefixes:   []string{"strict"},
			prefixesOK: true,
			wantErr:    "blocked-realm-prefixes",
		},
		{
			name:       "bad CIDR",
			prefixes:   []string{"10.0.0.0/33"},
			prefixesOK: true,
			wantErr:    "blocked-realm-prefixes",
		},
		{
			name:       "invalid hostname",
			prefixes:   []string{"evil.example.com"},
			prefixesOK: true,
			wantErr:    "blocked-realm-prefixes",
		},
		{
			name:       "none combined",
			prefixes:   []string{"none", "default"},
			prefixesOK: true,
			wantErr:    "blocked-realm-prefixes",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := Registry{Prefix: "example.com"}
			if tt.prefixesOK {
				reg.BlockedRealmPrefixes = tt.prefixes
			}
			cfg := &RegistriesConfig{
				Registries: []Registry{reg},
				Aliases:    map[string]string{},
			}

			props, err := NewRegistryProperties("example.com/image:v1", cfg)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatal("NewRegistryProperties() error = nil, want an error")
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %v, want it to mention %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("NewRegistryProperties() unexpected error: %v", err)
			}
			got := props.Attributes.BlockedRealmPrefixes
			if tt.wantNil {
				if got != nil {
					t.Errorf("Attributes.BlockedRealmPrefixes = %v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatal("Attributes.BlockedRealmPrefixes = nil, want non-nil")
			}
			if tt.wantEmpty {
				if len(got) != 0 {
					t.Errorf("Attributes.BlockedRealmPrefixes = %v, want empty", got)
				}
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Attributes.BlockedRealmPrefixes = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMergeRegistriesConfig_BlockedRealmPrefixes(t *testing.T) {
	base := &RegistriesConfig{
		Registries: []Registry{
			{
				Prefix:               "example.com",
				BlockedRealmPrefixes: []string{"default"},
			},
		},
		Aliases: map[string]string{},
	}

	t.Run("drop-in with only insecure drops denylist", func(t *testing.T) {
		dropIn := &RegistriesConfig{
			Registries: []Registry{
				{Prefix: "example.com", Insecure: true},
			},
			Aliases: map[string]string{},
		}
		result := mergeRegistriesConfig(base, dropIn)
		if result.Registries[0].BlockedRealmPrefixes != nil {
			t.Errorf("BlockedRealmPrefixes = %v, want nil after whole-entry replace", result.Registries[0].BlockedRealmPrefixes)
		}
	})

	t.Run("drop-in with empty array stays non-nil empty", func(t *testing.T) {
		dropIn := &RegistriesConfig{
			Registries: []Registry{
				{Prefix: "example.com", BlockedRealmPrefixes: []string{}},
			},
			Aliases: map[string]string{},
		}
		result := mergeRegistriesConfig(base, dropIn)
		p := result.Registries[0].BlockedRealmPrefixes
		if p == nil {
			t.Fatal("BlockedRealmPrefixes = nil, want non-nil empty slice")
		}
		if len(p) != 0 {
			t.Errorf("BlockedRealmPrefixes = %v, want empty", p)
		}
	})
}

func TestNewRegistryProperties_BlockedRealmPrefixes_registryInError(t *testing.T) {
	cfg := &RegistriesConfig{
		Registries: []Registry{
			{Prefix: "bad.example", BlockedRealmPrefixes: []string{"not-valid"}},
		},
		Aliases: map[string]string{},
	}
	_, err := NewRegistryProperties("bad.example/image:v1", cfg)
	if err == nil {
		t.Fatal("NewRegistryProperties() error = nil, want an error")
	}
	if !strings.Contains(err.Error(), `registry "bad.example"`) {
		t.Errorf("error = %v, want it to contain registry %q", err, "bad.example")
	}
}

func TestNewRegistryProperties_BlockedRealmPrefixes_mergeDropInSemantics(t *testing.T) {
	base := &RegistriesConfig{
		Registries: []Registry{
			{Prefix: "example.com", BlockedRealmPrefixes: []string{"default"}},
		},
		Aliases: map[string]string{},
	}
	dropIn := &RegistriesConfig{
		Registries: []Registry{
			{Prefix: "example.com", Insecure: true},
		},
		Aliases: map[string]string{},
	}
	merged := mergeRegistriesConfig(base, dropIn)

	props, err := NewRegistryProperties("example.com/image:v1", merged)
	if err != nil {
		t.Fatalf("NewRegistryProperties() error: %v", err)
	}
	if props.Attributes.BlockedRealmPrefixes != nil {
		t.Errorf("Attributes.BlockedRealmPrefixes = %v, want nil (library defaults)", props.Attributes.BlockedRealmPrefixes)
	}
}
