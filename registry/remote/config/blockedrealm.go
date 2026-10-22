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
	"fmt"
	"net/netip"

	"github.com/oras-project/oras-go/v3/registry/remote/auth"
)

// resolveBlockedRealmPrefixes expands registries.conf blocked-realm-prefixes
// entries into a concrete denylist. An empty input returns an empty non-nil slice.
func resolveBlockedRealmPrefixes(entries []string) ([]netip.Prefix, error) {
	if len(entries) == 0 {
		return []netip.Prefix{}, nil
	}

	if len(entries) == 1 && entries[0] == "none" {
		return []netip.Prefix{}, nil
	}

	var out []netip.Prefix
	for _, entry := range entries {
		if entry == "none" {
			return nil, fmt.Errorf("invalid blocked-realm-prefixes entry %q: \"none\" must be the only entry", entry)
		}
		switch entry {
		case "default":
			out = append(out, auth.DefaultBlockedRealmPrefixes()...)
		case "private":
			out = append(out, auth.PrivateRealmPrefixes()...)
		default:
			prefix, err := parseBlockedRealmPrefixEntry(entry)
			if err != nil {
				return nil, err
			}
			out = append(out, prefix)
		}
	}
	return out, nil
}

func parseBlockedRealmPrefixEntry(entry string) (netip.Prefix, error) {
	if prefix, err := netip.ParsePrefix(entry); err == nil {
		if prefix.Addr().Is4In6() {
			return netip.Prefix{}, fmt.Errorf("invalid blocked-realm-prefixes entry %q: write IPv4-mapped prefixes in IPv4 form", entry)
		}
		return prefix, nil
	}
	addr, err := netip.ParseAddr(entry)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("invalid blocked-realm-prefixes entry %q: not a CIDR prefix, IP address, or known name", entry)
	}
	// Unmap so an IPv4-mapped literal matches resolved IPv4 realm addresses.
	addr = addr.Unmap()
	bits := 32
	if addr.Is6() {
		bits = 128
	}
	return netip.PrefixFrom(addr, bits), nil
}
