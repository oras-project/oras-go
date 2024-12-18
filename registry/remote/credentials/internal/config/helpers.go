package config

import (
	"encoding/json"
	"fmt"

	"oras.land/oras-go/v2/registry/remote/auth"
)

// getCredentialsFromCache is a helper function to get the credential for serverAddress from authsRaw where key
// is an address and value is a raw JSON content.
func getCredentialFromAuthsRaw(authsRaw map[string]json.RawMessage, serverAddress string) (auth.Credential, error) {
	authCfgBytes, ok := authsRaw[serverAddress]
	if !ok {
		// NOTE: the auth key for the server address may have been stored with
		// a http/https prefix in legacy config files, e.g. "registry.example.com"
		// can be stored as "https://registry.example.com/".
		var matched bool
		for addr, auth := range authsRaw {
			if toHostname(addr) == serverAddress {
				matched = true
				authCfgBytes = auth
				break
			}
		}
		if !matched {
			return auth.EmptyCredential, nil
		}
	}
	var authCfg AuthConfig
	if err := json.Unmarshal(authCfgBytes, &authCfg); err != nil {
		return auth.EmptyCredential, fmt.Errorf("failed to unmarshal auth field: %w: %v", ErrInvalidConfigFormat, err)
	}
	return authCfg.Credential()
}
