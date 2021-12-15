package auth

import (
	"reflect"
	"testing"
)

func Test_parseChallenge(t *testing.T) {
	tests := []struct {
		name       string
		header     string
		wantScheme string
		wantParams map[string]string
	}{
		{
			name: "empty header",
		},
		{
			name:       "unknown scheme",
			header:     "foo bar",
			wantScheme: "foo",
		},
		{
			name:       "basic challenge",
			header:     `Basic realm="Test Registry"`,
			wantScheme: "basic",
		},
		{
			name:       "basic challenge with no parameters",
			header:     "Basic",
			wantScheme: "basic",
		},
		{
			name:       "basic challenge with no parameters but spaces",
			header:     "Basic  ",
			wantScheme: "basic",
		},
		{
			name:       "bearer challenge",
			header:     `Bearer realm="https://auth.example.io/token",service="registry.example.io",scope="repository:library/hello-world:pull,push"`,
			wantScheme: "bearer",
			wantParams: map[string]string{
				"realm":   "https://auth.example.io/token",
				"service": "registry.example.io",
				"scope":   "repository:library/hello-world:pull,push",
			},
		},
		{
			name:       "bearer challenge with multiple scopes",
			header:     `Bearer realm="https://auth.example.io/token",service="registry.example.io",scope="repository:library/alpine:pull,push repository:ubuntu:pull"`,
			wantScheme: "bearer",
			wantParams: map[string]string{
				"realm":   "https://auth.example.io/token",
				"service": "registry.example.io",
				"scope":   "repository:library/alpine:pull,push repository:ubuntu:pull",
			},
		},
		{
			name:       "bearer challenge with no parameters",
			header:     "Bearer",
			wantScheme: "bearer",
		},
		{
			name:       "bearer challenge with no parameters but spaces",
			header:     "Bearer  ",
			wantScheme: "bearer",
		},
		{
			name:       "bearer challenge with white spaces",
			header:     `Bearer realm = "https://auth.example.io/token"   ,service=registry.example.io, scope  ="repository:library/hello-world:pull,push"  `,
			wantScheme: "bearer",
			wantParams: map[string]string{
				"realm":   "https://auth.example.io/token",
				"service": "registry.example.io",
				"scope":   "repository:library/hello-world:pull,push",
			},
		},
		{
			name:       "bad bearer challenge (incomplete parameter with spaces)",
			header:     `Bearer realm="https://auth.example.io/token",service`,
			wantScheme: "bearer",
			wantParams: map[string]string{
				"realm": "https://auth.example.io/token",
			},
		},
		{
			name:       "bad bearer challenge (incomplete parameter with no value)",
			header:     `Bearer realm="https://auth.example.io/token",service=`,
			wantScheme: "bearer",
			wantParams: map[string]string{
				"realm": "https://auth.example.io/token",
			},
		},
		{
			name:       "bad bearer challenge (incomplete parameter with spaces)",
			header:     `Bearer realm="https://auth.example.io/token",service= `,
			wantScheme: "bearer",
			wantParams: map[string]string{
				"realm": "https://auth.example.io/token",
			},
		},
		{
			name:       "bad bearer challenge (incomplete quote)",
			header:     `Bearer realm="https://auth.example.io/token",service="registry`,
			wantScheme: "bearer",
			wantParams: map[string]string{
				"realm": "https://auth.example.io/token",
			},
		},
		{
			name:       "bearer challenge with empty parameter value",
			header:     `Bearer realm="https://auth.example.io/token",empty="",service="registry.example.io",scope="repository:library/hello-world:pull,push"`,
			wantScheme: "bearer",
			wantParams: map[string]string{
				"realm":   "https://auth.example.io/token",
				"empty":   "",
				"service": "registry.example.io",
				"scope":   "repository:library/hello-world:pull,push",
			},
		},
		{
			name:       "bearer challenge with escaping parameter value",
			header:     `Bearer foo="foo\"bar",hello="\"hello world\""`,
			wantScheme: "bearer",
			wantParams: map[string]string{
				"foo":   `foo"bar`,
				"hello": `"hello world"`,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotScheme, gotParams := parseChallenge(tt.header)
			if gotScheme != tt.wantScheme {
				t.Errorf("parseChallenge() gotScheme = %v, want %v", gotScheme, tt.wantScheme)
			}
			if !reflect.DeepEqual(gotParams, tt.wantParams) {
				t.Errorf("parseChallenge() gotParams = %v, want %v", gotParams, tt.wantParams)
			}
		})
	}
}
