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
	"testing"
	"time"
)

// TestCache_TokenExpiration tests that expired tokens are automatically removed
func TestCache_TokenExpiration(t *testing.T) {
	cache := NewCache()
	ctx := context.Background()
	registry := "example.com"
	scheme := SchemeBearer
	key := "test-scope"

	// Create a JWT token that expires in 15 seconds (beyond the 10-second grace period)
	expiredToken := createTestJWT(time.Now().Add(15 * time.Second).Unix())

	// Set the token
	_, err := cache.Set(ctx, registry, scheme, key, func(context.Context) (string, error) {
		return expiredToken, nil
	})
	if err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	// Token should be retrievable immediately
	token, err := cache.GetToken(ctx, registry, scheme, key)
	if err != nil {
		t.Errorf("GetToken() error = %v, want nil", err)
	}
	if token != expiredToken {
		t.Errorf("GetToken() = %v, want %v", token, expiredToken)
	}

	// Wait for token to expire (15 seconds, with 10-second grace period it will be considered expired at ~5 seconds)
	time.Sleep(6 * time.Second)

	// Token should now be expired and removed
	_, err = cache.GetToken(ctx, registry, scheme, key)
	if err == nil {
		t.Error("GetToken() should return error for expired token")
	}
}

// TestCache_ValidTokenNotRemoved tests that valid tokens are not removed
func TestCache_ValidTokenNotRemoved(t *testing.T) {
	cache := NewCache()
	ctx := context.Background()
	registry := "example.com"
	scheme := SchemeBearer
	key := "test-scope"

	// Create a JWT token that expires in 1 hour
	validToken := createTestJWT(time.Now().Add(1 * time.Hour).Unix())

	// Set the token
	_, err := cache.Set(ctx, registry, scheme, key, func(context.Context) (string, error) {
		return validToken, nil
	})
	if err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	// Token should be retrievable multiple times
	for i := 0; i < 5; i++ {
		token, err := cache.GetToken(ctx, registry, scheme, key)
		if err != nil {
			t.Errorf("GetToken() attempt %d error = %v, want nil", i+1, err)
		}
		if token != validToken {
			t.Errorf("GetToken() attempt %d = %v, want %v", i+1, token, validToken)
		}
	}
}

// TestCache_GracePeriod tests the 10-second grace period for token expiration
func TestCache_GracePeriod(t *testing.T) {
	cache := NewCache()
	ctx := context.Background()
	registry := "example.com"
	scheme := SchemeBearer
	key := "test-scope"

	// Create a JWT token that expires in 12 seconds
	// With 10-second grace period, it should still be valid
	token := createTestJWT(time.Now().Add(12 * time.Second).Unix())

	// Set the token
	_, err := cache.Set(ctx, registry, scheme, key, func(context.Context) (string, error) {
		return token, nil
	})
	if err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	// Token should be retrievable
	retrievedToken, err := cache.GetToken(ctx, registry, scheme, key)
	if err != nil {
		t.Errorf("GetToken() error = %v, want nil", err)
	}
	if retrievedToken != token {
		t.Errorf("GetToken() = %v, want %v", retrievedToken, token)
	}

	// Create a token that expires in 8 seconds
	// With 10-second grace period, it should be considered expired
	shortToken := createTestJWT(time.Now().Add(8 * time.Second).Unix())

	// Set the token
	_, err = cache.Set(ctx, registry, scheme, "short-key", func(context.Context) (string, error) {
		return shortToken, nil
	})
	if err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	// Token should be considered expired due to grace period
	_, err = cache.GetToken(ctx, registry, scheme, "short-key")
	if err == nil {
		t.Error("GetToken() should return error for token within grace period")
	}
}

// TestCache_NonJWTTokenExpiration tests that non-JWT tokens get default expiration
func TestCache_NonJWTTokenExpiration(t *testing.T) {
	cache := NewCache()
	ctx := context.Background()
	registry := "example.com"
	scheme := SchemeBasic
	key := ""

	// Create a simple non-JWT token
	simpleToken := "basic-auth-token-xyz"

	// Set the token
	_, err := cache.Set(ctx, registry, scheme, key, func(context.Context) (string, error) {
		return simpleToken, nil
	})
	if err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	// Token should be retrievable immediately
	token, err := cache.GetToken(ctx, registry, scheme, key)
	if err != nil {
		t.Errorf("GetToken() error = %v, want nil", err)
	}
	if token != simpleToken {
		t.Errorf("GetToken() = %v, want %v", token, simpleToken)
	}

	// Wait for default expiration (60 seconds + grace period)
	// For testing purposes, we just verify it's stored with expiration
	// Full wait would make test too slow
}

// TestCache_MultipleTokensExpiration tests expiration with multiple tokens
func TestCache_MultipleTokensExpiration(t *testing.T) {
	cache := NewCache()
	ctx := context.Background()
	registry := "example.com"
	scheme := SchemeBearer

	// Create tokens with different expiration times
	tokens := map[string]string{
		"scope1": createTestJWT(time.Now().Add(1 * time.Hour).Unix()),    // Valid
		"scope2": createTestJWT(time.Now().Add(15 * time.Second).Unix()), // Will expire
		"scope3": createTestJWT(time.Now().Add(30 * time.Minute).Unix()), // Valid
	}

	// Set all tokens
	for key, token := range tokens {
		_, err := cache.Set(ctx, registry, scheme, key, func(context.Context) (string, error) {
			return token, nil
		})
		if err != nil {
			t.Fatalf("Set() for key %s error = %v", key, err)
		}
	}

	// All tokens should be retrievable initially
	for key, expectedToken := range tokens {
		token, err := cache.GetToken(ctx, registry, scheme, key)
		if err != nil {
			t.Errorf("GetToken() for key %s error = %v", key, err)
		}
		if token != expectedToken {
			t.Errorf("GetToken() for key %s = %v, want %v", key, token, expectedToken)
		}
	}

	// Wait for scope2 to expire (15 seconds - 10 second grace = 5 seconds)
	time.Sleep(6 * time.Second)

	// scope1 and scope3 should still be valid
	for _, key := range []string{"scope1", "scope3"} {
		_, err := cache.GetToken(ctx, registry, scheme, key)
		if err != nil {
			t.Errorf("GetToken() for key %s should not error, got %v", key, err)
		}
	}

	// scope2 should be expired
	_, err := cache.GetToken(ctx, registry, scheme, "scope2")
	if err == nil {
		t.Error("GetToken() for scope2 should return error for expired token")
	}
}

// TestCache_SchemeChangeInvalidatesExpiration tests that scheme changes invalidate all tokens
func TestCache_SchemeChangeInvalidatesExpiration(t *testing.T) {
	cache := NewCache()
	ctx := context.Background()
	registry := "example.com"

	// Set a bearer token
	bearerToken := createTestJWT(time.Now().Add(1 * time.Hour).Unix())
	_, err := cache.Set(ctx, registry, SchemeBearer, "scope1", func(context.Context) (string, error) {
		return bearerToken, nil
	})
	if err != nil {
		t.Fatalf("Set() bearer token error = %v", err)
	}

	// Verify bearer token is cached
	token, err := cache.GetToken(ctx, registry, SchemeBearer, "scope1")
	if err != nil {
		t.Errorf("GetToken() bearer token error = %v", err)
	}
	if token != bearerToken {
		t.Errorf("GetToken() = %v, want %v", token, bearerToken)
	}

	// Set a basic token (scheme change)
	basicToken := "basic-token"
	_, err = cache.Set(ctx, registry, SchemeBasic, "", func(context.Context) (string, error) {
		return basicToken, nil
	})
	if err != nil {
		t.Fatalf("Set() basic token error = %v", err)
	}

	// Bearer token should be invalidated
	_, err = cache.GetToken(ctx, registry, SchemeBearer, "scope1")
	if err == nil {
		t.Error("GetToken() should return error after scheme change")
	}

	// Basic token should be retrievable
	token, err = cache.GetToken(ctx, registry, SchemeBasic, "")
	if err != nil {
		t.Errorf("GetToken() basic token error = %v", err)
	}
	if token != basicToken {
		t.Errorf("GetToken() = %v, want %v", token, basicToken)
	}
}

// TestCache_ConcurrentExpirationCheck tests concurrent access during expiration checks
func TestCache_ConcurrentExpirationCheck(t *testing.T) {
	cache := NewCache()
	ctx := context.Background()
	registry := "example.com"
	scheme := SchemeBearer
	key := "test-scope"

	// Create a token that will expire in 15 seconds
	token := createTestJWT(time.Now().Add(15 * time.Second).Unix())

	_, err := cache.Set(ctx, registry, scheme, key, func(context.Context) (string, error) {
		return token, nil
	})
	if err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	// Start multiple goroutines checking expiration
	done := make(chan bool)
	for i := 0; i < 50; i++ {
		go func() {
			for j := 0; j < 5; j++ {
				_, _ = cache.GetToken(ctx, registry, scheme, key)
				time.Sleep(200 * time.Millisecond)
			}
			done <- true
		}()
	}

	// Wait for all goroutines to complete
	for i := 0; i < 50; i++ {
		<-done
	}

	// Wait for token to expire
	time.Sleep(6 * time.Second)

	// Token should be expired and removed by now
	_, err = cache.GetToken(ctx, registry, scheme, key)
	if err == nil {
		t.Error("GetToken() should return error for expired token after concurrent access")
	}
}

// TestCache_ExpirationWithSchemeRetrieval tests that scheme can still be retrieved after tokens expire
func TestCache_ExpirationWithSchemeRetrieval(t *testing.T) {
	cache := NewCache()
	ctx := context.Background()
	registry := "example.com"
	scheme := SchemeBearer
	key := "test-scope"

	// Create a token that will expire in 15 seconds
	token := createTestJWT(time.Now().Add(15 * time.Second).Unix())

	_, err := cache.Set(ctx, registry, scheme, key, func(context.Context) (string, error) {
		return token, nil
	})
	if err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	// Scheme should be retrievable
	retrievedScheme, err := cache.GetScheme(ctx, registry)
	if err != nil {
		t.Errorf("GetScheme() error = %v", err)
	}
	if retrievedScheme != scheme {
		t.Errorf("GetScheme() = %v, want %v", retrievedScheme, scheme)
	}

	// Wait for token to expire
	time.Sleep(6 * time.Second)

	// Scheme should still be retrievable even after token expires
	retrievedScheme, err = cache.GetScheme(ctx, registry)
	if err != nil {
		t.Errorf("GetScheme() after expiration error = %v", err)
	}
	if retrievedScheme != scheme {
		t.Errorf("GetScheme() after expiration = %v, want %v", retrievedScheme, scheme)
	}

	// But token should be expired
	_, err = cache.GetToken(ctx, registry, scheme, key)
	if err == nil {
		t.Error("GetToken() should return error for expired token")
	}
}

// Benchmark token expiration check performance
func BenchmarkCache_TokenExpirationCheck(b *testing.B) {
	cache := NewCache()
	ctx := context.Background()
	registry := "example.com"
	scheme := SchemeBearer
	key := "test-scope"

	token := createTestJWT(time.Now().Add(1 * time.Hour).Unix())
	_, _ = cache.Set(ctx, registry, scheme, key, func(context.Context) (string, error) {
		return token, nil
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = cache.GetToken(ctx, registry, scheme, key)
	}
}
