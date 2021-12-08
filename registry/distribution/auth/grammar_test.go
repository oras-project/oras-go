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
			name:       "bad bearer challenge",
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

func Test_parseQuotedString(t *testing.T) {
	tests := []struct {
		name      string
		s         string
		wantValue string
		wantRest  string
	}{
		{
			name: "empty string",
		},
		{
			name:     "not quoted",
			s:        "hello world",
			wantRest: "hello world",
		},
		{
			name:     "half quoted",
			s:        `"hello world`,
			wantRest: `"hello world`,
		},
		{
			name:      "quoted string",
			s:         `"hello world"`,
			wantValue: "hello world",
		},
		{
			name: "quoted empty string",
			s:    `""`,
		},
		{
			name:      "quoted string with tail",
			s:         `"hello world" foo bar`,
			wantValue: "hello world",
			wantRest:  " foo bar",
		},
		{
			name:      "quoted string with tail and no space",
			s:         `"hello world"foo bar`,
			wantValue: "hello world",
			wantRest:  "foo bar",
		},
		{
			name:      "quoted string with escaped characters",
			s:         `"he\\llo\" world"`,
			wantValue: `he\llo" world`,
		},
		{
			name:     "half quoted with escaped characters",
			s:        `"hello world\"`,
			wantRest: `"hello world\"`,
		},
		{
			name:      "quoted escaping characters",
			s:         `"\\\\\\\\"`,
			wantValue: `\\\\`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotValue, gotRest := parseQuotedString(tt.s)
			if gotValue != tt.wantValue {
				t.Errorf("parseQuotedString() gotValue = %v, want %v", gotValue, tt.wantValue)
			}
			if gotRest != tt.wantRest {
				t.Errorf("parseQuotedString() gotRest = %v, want %v", gotRest, tt.wantRest)
			}
		})
	}
}
