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

package properties

import "testing"

func TestTransport_Fields(t *testing.T) {
	transport := Transport{
		CACert:    "/path/to/ca.crt",
		Cert:      "/path/to/client.crt",
		Key:       "/path/to/client.key",
		Insecure:  true,
		PlainHTTP: true,
		HeaderFlags: map[string]string{
			"X-Custom-Header":  "custom-value",
			"X-Another-Header": "another-value",
		},
	}

	if transport.CACert != "/path/to/ca.crt" {
		t.Errorf("CACert = %q, want %q", transport.CACert, "/path/to/ca.crt")
	}

	if transport.Cert != "/path/to/client.crt" {
		t.Errorf("Cert = %q, want %q", transport.Cert, "/path/to/client.crt")
	}

	if transport.Key != "/path/to/client.key" {
		t.Errorf("Key = %q, want %q", transport.Key, "/path/to/client.key")
	}

	if !transport.Insecure {
		t.Error("Insecure = false, want true")
	}

	if !transport.PlainHTTP {
		t.Error("PlainHTTP = false, want true")
	}

	if len(transport.HeaderFlags) != 2 {
		t.Errorf("HeaderFlags length = %d, want 2", len(transport.HeaderFlags))
	}

	if transport.HeaderFlags["X-Custom-Header"] != "custom-value" {
		t.Errorf("HeaderFlags[X-Custom-Header] = %q, want %q",
			transport.HeaderFlags["X-Custom-Header"], "custom-value")
	}

	if transport.HeaderFlags["X-Another-Header"] != "another-value" {
		t.Errorf("HeaderFlags[X-Another-Header] = %q, want %q",
			transport.HeaderFlags["X-Another-Header"], "another-value")
	}
}

func TestTransport_Defaults(t *testing.T) {
	transport := Transport{}

	if transport.CACert != "" {
		t.Errorf("CACert = %q, want empty string", transport.CACert)
	}

	if transport.Cert != "" {
		t.Errorf("Cert = %q, want empty string", transport.Cert)
	}

	if transport.Key != "" {
		t.Errorf("Key = %q, want empty string", transport.Key)
	}

	if transport.Insecure {
		t.Error("Insecure = true, want false")
	}

	if transport.PlainHTTP {
		t.Error("PlainHTTP = true, want false")
	}

	if transport.HeaderFlags != nil {
		t.Errorf("HeaderFlags = %v, want nil", transport.HeaderFlags)
	}
}

func TestTransport_SecureHTTPS(t *testing.T) {
	// Test configuration for secure HTTPS with CA certificate
	transport := Transport{
		CACert:    "/etc/ssl/certs/ca.crt",
		Insecure:  false,
		PlainHTTP: false,
	}

	if transport.Insecure {
		t.Error("Insecure should be false for secure HTTPS")
	}

	if transport.PlainHTTP {
		t.Error("PlainHTTP should be false for HTTPS")
	}

	if transport.CACert != "/etc/ssl/certs/ca.crt" {
		t.Errorf("CACert = %q, want %q", transport.CACert, "/etc/ssl/certs/ca.crt")
	}
}

func TestTransport_InsecureHTTPS(t *testing.T) {
	// Test configuration for insecure HTTPS (skip certificate verification)
	transport := Transport{
		Insecure:  true,
		PlainHTTP: false,
	}

	if !transport.Insecure {
		t.Error("Insecure should be true for insecure HTTPS")
	}

	if transport.PlainHTTP {
		t.Error("PlainHTTP should be false even when Insecure is true")
	}
}

func TestTransport_PlainHTTP(t *testing.T) {
	// Test configuration for plain HTTP
	transport := Transport{
		PlainHTTP: true,
		Insecure:  false,
	}

	if !transport.PlainHTTP {
		t.Error("PlainHTTP should be true for HTTP connections")
	}

	if transport.Insecure {
		t.Error("Insecure should be false when using plain HTTP")
	}
}

func TestTransport_MutualTLS(t *testing.T) {
	// Test configuration for mutual TLS authentication
	transport := Transport{
		CACert: "/etc/ssl/certs/ca.crt",
		Cert:   "/etc/ssl/certs/client.crt",
		Key:    "/etc/ssl/private/client.key",
	}

	if transport.CACert != "/etc/ssl/certs/ca.crt" {
		t.Errorf("CACert = %q, want %q", transport.CACert, "/etc/ssl/certs/ca.crt")
	}

	if transport.Cert != "/etc/ssl/certs/client.crt" {
		t.Errorf("Cert = %q, want %q", transport.Cert, "/etc/ssl/certs/client.crt")
	}

	if transport.Key != "/etc/ssl/private/client.key" {
		t.Errorf("Key = %q, want %q", transport.Key, "/etc/ssl/private/client.key")
	}
}

func TestTransport_CustomHeaders(t *testing.T) {
	tests := []struct {
		name        string
		headerFlags map[string]string
	}{
		{
			name:        "empty headers",
			headerFlags: map[string]string{},
		},
		{
			name: "single header",
			headerFlags: map[string]string{
				"User-Agent": "oras-go/1.0",
			},
		},
		{
			name: "multiple headers",
			headerFlags: map[string]string{
				"User-Agent":      "oras-go/1.0",
				"X-Custom-Header": "custom-value",
				"Authorization":   "Bearer token",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transport := Transport{
				HeaderFlags: tt.headerFlags,
			}

			if len(transport.HeaderFlags) != len(tt.headerFlags) {
				t.Errorf("HeaderFlags length = %d, want %d", len(transport.HeaderFlags), len(tt.headerFlags))
			}

			for key, expectedValue := range tt.headerFlags {
				if actualValue, ok := transport.HeaderFlags[key]; !ok {
					t.Errorf("HeaderFlags missing key %q", key)
				} else if actualValue != expectedValue {
					t.Errorf("HeaderFlags[%q] = %q, want %q", key, actualValue, expectedValue)
				}
			}
		})
	}
}
