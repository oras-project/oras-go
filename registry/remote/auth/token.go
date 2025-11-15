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
	"strings"
	"time"
)

const (
	gracePeriodSeconds       = 10
	defaultExpirationSeconds = 60
)

// tokenEntry represents a cached token with its expiration time.
type tokenEntry struct {
	// token is the actual authentication token
	token string
	// expiresAt is when the token expires
	expiresAt time.Time
}

// jwtClaims represents the standard claims in a JWT token.
// We only parse the fields we need for expiration handling.
type jwtClaims struct {
	// Exp is the expiration time (seconds since Unix epoch).
	Exp int64 `json:"exp"`
	// Iat is the issued at time (seconds since Unix epoch).
	Iat int64 `json:"iat,omitempty"`
	// Nbf is the not before time (seconds since Unix epoch).
	Nbf int64 `json:"nbf,omitempty"`
}

// parseTokenExpiration attempts to extract the expiration time from a token.
// It supports JWT tokens used in bearer auth. For non-JWT tokens or JWT tokens
// that don't contain expiration information (like basic auth tokens or JWTs without
// an expiration claim), it returns a default expiration of 60 seconds from now.
func parseTokenExpiration(token string) time.Time {
	// Try to parse as JWT token
	// JWT format: header.payload.signature
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		// Not a JWT token, return default expiration
		return time.Now().Add(defaultExpirationSeconds * time.Second)
	}

	// Decode the payload (second part)
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		// Failed to decode, return default expiration
		return time.Now().Add(defaultExpirationSeconds * time.Second)
	}

	// Parse the claims
	var claims jwtClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		// Failed to parse, return default expiration
		return time.Now().Add(defaultExpirationSeconds * time.Second)
	}

	// Check if expiration claim exists
	if claims.Exp > 0 {
		return time.Unix(claims.Exp, 0)
	}

	// No expiration claim, return default expiration
	return time.Now().Add(defaultExpirationSeconds * time.Second)
}

// isExpired checks if a token entry has expired.
// It includes a grace period to avoid using tokens that are
// about to expire.
func (te *tokenEntry) isExpired() bool {
	if te.expiresAt.IsZero() {
		// No expiration set, consider it never expires
		return false
	}
	// Add grace period
	return time.Now().Add(gracePeriodSeconds * time.Second).After(te.expiresAt)
}

// newTokenEntry creates a new token entry with the given token.
// It automatically extracts the expiration time from JWT tokens.
func newTokenEntry(token string) *tokenEntry {
	return &tokenEntry{
		token:     token,
		expiresAt: parseTokenExpiration(token),
	}
}
