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

package urlutil

import (
	"net/url"
	"testing"
)

func parseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("failed to parse URL %q: %v", raw, err)
	}
	return u
}

func TestCanonicalHostPort(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		// Case-insensitivity
		{
			name: "uppercase host default port",
			raw:  "http://REGISTRY.EXAMPLE.COM",
			want: "registry.example.com:80",
		},
		{
			name: "uppercase host explicit port",
			raw:  "http://REGISTRY.EXAMPLE.COM:80",
			want: "registry.example.com:80",
		},
		{
			name: "mixed case https",
			raw:  "https://Registry.Example.COM",
			want: "registry.example.com:443",
		},

		// Default vs explicit ports
		{
			name: "http default port omitted",
			raw:  "http://example.com",
			want: "example.com:80",
		},
		{
			name: "http explicit port 80",
			raw:  "http://example.com:80",
			want: "example.com:80",
		},
		{
			name: "https default port omitted",
			raw:  "https://example.com",
			want: "example.com:443",
		},
		{
			name: "https explicit port 443",
			raw:  "https://example.com:443",
			want: "example.com:443",
		},

		// Non-default ports
		{
			name: "http non-default port",
			raw:  "http://example.com:5000",
			want: "example.com:5000",
		},
		{
			name: "https non-default port",
			raw:  "https://example.com:8443",
			want: "example.com:8443",
		},

		// IPv6 hosts
		{
			name: "ipv6 localhost http default port",
			raw:  "http://[::1]",
			want: "[::1]:80",
		},
		{
			name: "ipv6 localhost http explicit port 80",
			raw:  "http://[::1]:80",
			want: "[::1]:80",
		},
		{
			name: "ipv6 localhost https default port",
			raw:  "https://[::1]",
			want: "[::1]:443",
		},
		{
			name: "ipv6 localhost https explicit port 443",
			raw:  "https://[::1]:443",
			want: "[::1]:443",
		},
		{
			name: "ipv6 non-default port",
			raw:  "https://[::1]:8443",
			want: "[::1]:8443",
		},
		{
			name: "ipv6 full address mixed case default port",
			raw:  "https://[2001:DB8::1]",
			want: "[2001:db8::1]:443",
		},
		{
			name: "ipv6 full address explicit port 443",
			raw:  "https://[2001:db8::1]:443",
			want: "[2001:db8::1]:443",
		},
		{
			name: "ipv6 uncompressed zeroes localhost default port",
			raw:  "https://[0:0:0:0:0:0:0:1]",
			want: "[::1]:443",
		},
		{
			name: "ipv6 uncompressed zeroes localhost explicit port 443",
			raw:  "https://[0:0:0:0:0:0:0:1]:443",
			want: "[::1]:443",
		},
		{
			name: "ipv6 uncompressed zeroes localhost non-default port",
			raw:  "https://[0:0:0:0:0:0:0:1]:8080",
			want: "[::1]:8080",
		},
		{
			name: "ipv6 uppercase hex with port",
			raw:  "https://[2001:DB8::FF]:443",
			want: "[2001:db8::ff]:443",
		},
		{
			name: "ipv6 lowercase hex default port",
			raw:  "https://[2001:db8::ff]",
			want: "[2001:db8::ff]:443",
		},
		{
			name: "ipv6 leading zeroes in hextets",
			raw:  "https://[2001:0db8:0000:0000:0000:0000:0000:0001]",
			want: "[2001:db8::1]:443",
		},
		{
			name: "ipv6 zone preserved without lower-casing",
			raw:  "https://[fe80::1%25ETH0]:443",
			want: "[fe80::1%ETH0]:443",
		},
		{
			name: "ipv4 localhost default port",
			raw:  "http://127.0.0.1",
			want: "127.0.0.1:80",
		},
		{
			name: "ipv4 localhost explicit port 80",
			raw:  "http://127.0.0.1:80",
			want: "127.0.0.1:80",
		},
		{
			name: "ipv4 mapped ipv6 default port",
			raw:  "https://[::ffff:127.0.0.1]",
			want: "[::ffff:127.0.0.1]:443",
		},
		{
			name: "ipv4 mapped ipv6 uncompressed explicit port 443",
			raw:  "https://[0:0:0:0:0:ffff:127.0.0.1]:443",
			want: "[::ffff:127.0.0.1]:443",
		},

		// Non-HTTP schemes
		{
			name: "non-http scheme with port",
			raw:  "ftp://example.com:21",
			want: "example.com:21",
		},
		{
			name: "non-http scheme without port",
			raw:  "ftp://example.com",
			want: "example.com:",
		},
		{
			name: "non-http scheme uppercase host without port",
			raw:  "ftp://EXAMPLE.COM",
			want: "example.com:",
		},
		{
			name: "non-http scheme ipv6 with port",
			raw:  "custom://[::1]:9000",
			want: "[::1]:9000",
		},
		{
			name: "non-http scheme ipv6 without port",
			raw:  "custom://[::1]",
			want: "[::1]:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := parseURL(t, tt.raw)
			if got := CanonicalHostPort(u); got != tt.want {
				t.Errorf("CanonicalHostPort(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}

	t.Run("nil URL", func(t *testing.T) {
		if got := CanonicalHostPort(nil); got != "" {
			t.Errorf("CanonicalHostPort(nil) = %q, want empty string", got)
		}
	})
}

func TestSameOrigin(t *testing.T) {
	tests := []struct {
		name string
		a    string
		b    string
		want bool
	}{
		// Case-insensitivity
		{
			name: "case-insensitive host",
			a:    "http://REGISTRY.EXAMPLE.COM",
			b:    "http://registry.example.com",
			want: true,
		},
		{
			name: "case-insensitive scheme",
			a:    "HTTP://registry.example.com",
			b:    "http://registry.example.com",
			want: true,
		},
		{
			name: "case-insensitive both host and scheme",
			a:    "HTTPS://REGISTRY.EXAMPLE.COM",
			b:    "https://registry.example.com",
			want: true,
		},

		// Default vs explicit ports
		{
			name: "http default vs explicit port 80",
			a:    "http://example.com",
			b:    "http://example.com:80",
			want: true,
		},
		{
			name: "https default vs explicit port 443",
			a:    "https://example.com",
			b:    "https://example.com:443",
			want: true,
		},
		{
			name: "scheme mismatch",
			a:    "http://example.com",
			b:    "https://example.com",
			want: false,
		},
		{
			name: "different host",
			a:    "https://example.com",
			b:    "https://evil.com",
			want: false,
		},

		// Non-default ports
		{
			name: "identical non-default port",
			a:    "http://example.com:5000",
			b:    "http://example.com:5000",
			want: true,
		},
		{
			name: "different non-default port",
			a:    "http://example.com:5000",
			b:    "http://example.com:6000",
			want: false,
		},
		{
			name: "explicit standard port vs non-default port",
			a:    "https://example.com:443",
			b:    "https://example.com:8443",
			want: false,
		},

		// IPv6 hosts
		{
			name: "ipv6 localhost default vs explicit port",
			a:    "http://[::1]",
			b:    "http://[::1]:80",
			want: true,
		},
		{
			name: "ipv6 localhost https default vs explicit port",
			a:    "https://[::1]",
			b:    "https://[::1]:443",
			want: true,
		},
		{
			name: "ipv6 mixed case and explicit port",
			a:    "https://[2001:DB8::1]",
			b:    "https://[2001:db8::1]:443",
			want: true,
		},
		{
			name: "ipv6 non-default port identical",
			a:    "https://[2001:db8::1]:8443",
			b:    "https://[2001:db8::1]:8443",
			want: true,
		},
		{
			name: "ipv6 non-default port different",
			a:    "https://[2001:db8::1]:8443",
			b:    "https://[2001:db8::1]:9443",
			want: false,
		},
		{
			name: "ipv6 equivalent localhost default vs explicit port",
			a:    "https://[::1]/",
			b:    "https://[0:0:0:0:0:0:0:1]:443/",
			want: true,
		},
		{
			name: "ipv6 equivalent localhost non-default port",
			a:    "https://[::1]:8080/",
			b:    "https://[0:0:0:0:0:0:0:1]:8080/",
			want: true,
		},
		{
			name: "ipv6 hex case insensitivity default vs explicit port",
			a:    "https://[2001:db8::ff]/",
			b:    "https://[2001:DB8::FF]:443/",
			want: true,
		},
		{
			name: "ipv6 leading zeroes in hextets equivalent",
			a:    "https://[2001:0db8:0000:0000:0000:0000:0000:0001]",
			b:    "https://[2001:db8::1]:443",
			want: true,
		},
		{
			name: "ipv6 equivalent localhost different port",
			a:    "https://[::1]:8080/",
			b:    "https://[0:0:0:0:0:0:0:1]:9090/",
			want: false,
		},
		{
			name: "ipv6 zone identifier case preserved",
			a:    "https://[fe80::1%25ETH0]:443",
			b:    "https://[FE80::1%25ETH0]",
			want: true,
		},
		{
			name: "ipv6 different zone identifiers",
			a:    "https://[fe80::1%25eth0]:443",
			b:    "https://[fe80::1%25eth1]:443",
			want: false,
		},
		{
			name: "ipv4 localhost default vs explicit port",
			a:    "http://127.0.0.1",
			b:    "http://127.0.0.1:80",
			want: true,
		},
		{
			name: "ipv4 mapped ipv6 equivalent forms",
			a:    "https://[::ffff:127.0.0.1]",
			b:    "https://[0:0:0:0:0:ffff:127.0.0.1]:443",
			want: true,
		},

		// Non-HTTP schemes
		{
			name: "non-http identical with port",
			a:    "ftp://example.com:21",
			b:    "ftp://example.com:21",
			want: true,
		},
		{
			name: "non-http case-insensitive host without port",
			a:    "ftp://EXAMPLE.COM",
			b:    "ftp://example.com",
			want: true,
		},
		{
			name: "non-http different port",
			a:    "ftp://example.com:21",
			b:    "ftp://example.com:22",
			want: false,
		},
		{
			name: "non-http explicit port vs omitted port",
			a:    "ftp://example.com:21",
			b:    "ftp://example.com",
			want: false,
		},
		{
			name: "non-http ipv6 same",
			a:    "custom://[::1]",
			b:    "custom://[::1]",
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := parseURL(t, tt.a)
			b := parseURL(t, tt.b)
			if got := SameOrigin(a, b); got != tt.want {
				t.Errorf("SameOrigin(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}

	t.Run("nil URLs", func(t *testing.T) {
		u := parseURL(t, "https://example.com")
		if got := SameOrigin(nil, nil); !got {
			t.Errorf("SameOrigin(nil, nil) = %v, want true", got)
		}
		if got := SameOrigin(u, nil); got {
			t.Errorf("SameOrigin(u, nil) = %v, want false", got)
		}
		if got := SameOrigin(nil, u); got {
			t.Errorf("SameOrigin(nil, u) = %v, want false", got)
		}
	})
}

func TestSameHostname(t *testing.T) {
	tests := []struct {
		name string
		a    string
		b    string
		want bool
	}{
		{
			name: "same hostname different scheme and port",
			a:    "http://REGISTRY.EXAMPLE.COM:80",
			b:    "https://registry.example.com:443",
			want: true,
		},
		{
			name: "same hostname mixed case",
			a:    "https://Registry.Example.Com",
			b:    "https://registry.example.com",
			want: true,
		},
		{
			name: "different hostname",
			a:    "https://registry.example.com",
			b:    "https://other.example.com",
			want: false,
		},
		{
			name: "ipv6 hostname identical",
			a:    "http://[::1]:80",
			b:    "https://[::1]:443",
			want: true,
		},
		{
			name: "ipv6 hostname mixed case",
			a:    "https://[2001:DB8::1]",
			b:    "https://[2001:db8::1]",
			want: true,
		},
		{
			name: "ipv6 hostname different",
			a:    "https://[2001:db8::1]",
			b:    "https://[2001:db8::2]",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := parseURL(t, tt.a)
			b := parseURL(t, tt.b)
			if got := SameHostname(a, b); got != tt.want {
				t.Errorf("SameHostname(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}

	t.Run("nil URLs", func(t *testing.T) {
		u := parseURL(t, "https://example.com")
		if got := SameHostname(nil, nil); !got {
			t.Errorf("SameHostname(nil, nil) = %v, want true", got)
		}
		if got := SameHostname(u, nil); got {
			t.Errorf("SameHostname(u, nil) = %v, want false", got)
		}
		if got := SameHostname(nil, u); got {
			t.Errorf("SameHostname(nil, u) = %v, want false", got)
		}
	})
}
