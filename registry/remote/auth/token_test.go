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
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// createTestJWT creates a JWT token with the given expiration time for testing
func createTestJWT(exp int64) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))

	claims := map[string]interface{}{
		"exp": exp,
		"iat": time.Now().Unix(),
		"sub": "test",
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		panic(fmt.Sprintf("failed to marshal claims: %v", err))
	}
	payload := base64.RawURLEncoding.EncodeToString(claimsJSON)

	// We don't need a real signature for testing expiration parsing
	signature := base64.RawURLEncoding.EncodeToString([]byte("test-signature"))

	return fmt.Sprintf("%s.%s.%s", header, payload, signature)
}

func TestParseTokenExpiration(t *testing.T) {
	tests := []struct {
		name          string
		token         string
		wantAfter     time.Time
		wantBefore    time.Time
		expectDefault bool
	}{
		{
			name:          "valid JWT with future expiration",
			token:         createTestJWT(time.Now().Add(1 * time.Hour).Unix()),
			wantAfter:     time.Now().Add(59 * time.Minute),
			wantBefore:    time.Now().Add(61 * time.Minute),
			expectDefault: false,
		},
		{
			name:          "valid JWT with past expiration",
			token:         createTestJWT(time.Now().Add(-1 * time.Hour).Unix()),
			wantAfter:     time.Now().Add(-61 * time.Minute),
			wantBefore:    time.Now().Add(-59 * time.Minute),
			expectDefault: false,
		},
		{
			name:          "non-JWT token",
			token:         "simple-token-without-dots",
			wantAfter:     time.Now().Add(50 * time.Second),
			wantBefore:    time.Now().Add(70 * time.Second),
			expectDefault: true,
		},
		{
			name:          "malformed JWT",
			token:         "header.invalid-base64.signature",
			wantAfter:     time.Now().Add(50 * time.Second),
			wantBefore:    time.Now().Add(70 * time.Second),
			expectDefault: true,
		},
		{
			name:          "JWT without exp claim",
			token:         base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256"}`)) + "." + base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"test"}`)) + ".sig",
			wantAfter:     time.Now().Add(50 * time.Second),
			wantBefore:    time.Now().Add(70 * time.Second),
			expectDefault: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseTokenExpiration(tt.token)

			if got.Before(tt.wantAfter) {
				t.Errorf("parseTokenExpiration() = %v, want after %v", got, tt.wantAfter)
			}
			if got.After(tt.wantBefore) {
				t.Errorf("parseTokenExpiration() = %v, want before %v", got, tt.wantBefore)
			}
		})
	}
}

func TestTokenEntry_IsExpired(t *testing.T) {
	tests := []struct {
		name        string
		expiresAt   time.Time
		wantExpired bool
	}{
		{
			name:        "expired token",
			expiresAt:   time.Now().Add(-1 * time.Hour),
			wantExpired: true,
		},
		{
			name:        "valid token",
			expiresAt:   time.Now().Add(1 * time.Hour),
			wantExpired: false,
		},
		{
			name:        "token expiring soon (within grace period)",
			expiresAt:   time.Now().Add(5 * time.Second),
			wantExpired: true,
		},
		{
			name:        "token expiring after grace period",
			expiresAt:   time.Now().Add(15 * time.Second),
			wantExpired: false,
		},
		{
			name:        "zero expiration (never expires)",
			expiresAt:   time.Time{},
			wantExpired: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			te := &tokenEntry{
				token:     "test-token",
				expiresAt: tt.expiresAt,
			}

			if got := te.isExpired(); got != tt.wantExpired {
				t.Errorf("isExpired() = %v, want %v", got, tt.wantExpired)
			}
		})
	}
}

func TestNewTokenEntry(t *testing.T) {
	tests := []struct {
		name  string
		token string
	}{
		{
			name:  "JWT token",
			token: createTestJWT(time.Now().Add(1 * time.Hour).Unix()),
		},
		{
			name:  "simple token",
			token: "simple-token",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			te := newTokenEntry(tt.token)

			if te.token != tt.token {
				t.Errorf("newTokenEntry() token = %v, want %v", te.token, tt.token)
			}

			if te.expiresAt.IsZero() {
				t.Error("newTokenEntry() expiresAt should not be zero")
			}
		})
	}
}

func TestTokenEntry_ExpirationGracePeriod(t *testing.T) {
	// Create a token that expires in 9 seconds (within the 10-second grace period)
	te := &tokenEntry{
		token:     "test-token",
		expiresAt: time.Now().Add(9 * time.Second),
	}

	// With 10-second grace period, this should be considered expired
	if !te.isExpired() {
		t.Error("token expiring in 9 seconds should be considered expired due to grace period")
	}

	// Create a token that expires in 11 seconds
	te2 := &tokenEntry{
		token:     "test-token",
		expiresAt: time.Now().Add(11 * time.Second),
	}

	// This should not be expired
	if te2.isExpired() {
		t.Error("token expiring in 11 seconds should not be considered expired")
	}
}

func TestParseTokenExpiration_EdgeCases(t *testing.T) {
	tests := []struct {
		name  string
		token string
	}{
		{
			name:  "empty token",
			token: "",
		},
		{
			name:  "token with only dots",
			token: "..",
		},
		{
			name:  "token with many dots",
			token: "a.b.c.d.e",
		},
		{
			name:  "token with special characters",
			token: "header!@#$.payload$%^&.signature*()",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Should not panic
			got := parseTokenExpiration(tt.token)

			// Should return a valid time (default expiration)
			if got.IsZero() {
				t.Error("parseTokenExpiration() should not return zero time")
			}

			// Should be in the near future (default expiration is 60 seconds)
			if got.Before(time.Now()) {
				t.Error("parseTokenExpiration() should return future time for invalid tokens")
			}
			if got.After(time.Now().Add(2 * time.Minute)) {
				t.Error("parseTokenExpiration() should return default expiration (~60s) for invalid tokens")
			}
		})
	}
}

func TestTokenEntry_ConcurrentAccess(t *testing.T) {
	te := &tokenEntry{
		token:     "test-token",
		expiresAt: time.Now().Add(1 * time.Hour),
	}

	// Test concurrent reads
	done := make(chan bool)
	for i := 0; i < 100; i++ {
		go func() {
			_ = te.isExpired()
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 100; i++ {
		<-done
	}
}

func TestCreateTestJWT(t *testing.T) {
	// Verify our test helper creates valid JWT structure
	exp := time.Now().Add(1 * time.Hour).Unix()
	token := createTestJWT(exp)

	parts := splitJWT(token)
	if len(parts) != 3 {
		t.Errorf("createTestJWT() should create 3-part token, got %d parts", len(parts))
	}

	// Verify the expiration is correctly embedded
	expiresAt := parseTokenExpiration(token)
	expectedTime := time.Unix(exp, 0)

	// Allow 1 second difference due to timing
	if expiresAt.Sub(expectedTime) > time.Second || expectedTime.Sub(expiresAt) > time.Second {
		t.Errorf("createTestJWT() expiration = %v, want %v", expiresAt, expectedTime)
	}
}

// splitJWT is a helper to split JWT tokens
func splitJWT(token string) []string {
	return strings.Split(token, ".")
}
