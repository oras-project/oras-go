package auth

import (
	"strconv"
	"strings"
)

// parseChallenge parses the "WWW-Authenticate" header returned by the remote
// registry, and extracts parameters if scheme is Bearer.
// References:
// - https://docs.docker.com/registry/spec/auth/token/#how-to-authenticate
// - https://tools.ietf.org/html/rfc7235#section-2.1
func parseChallenge(header string) (scheme string, params map[string]string) {
	// as defined in RFC 7235 section 2.1, we have
	//     challenge   = auth-scheme [ 1*SP ( token68 / #auth-param ) ]
	//     auth-scheme = token
	//     auth-param  = token BWS "=" BWS ( token / quoted-string )
	//
	// since we focus parameters only on Bearer, we have
	//     challenge   = auth-scheme [ 1*SP #auth-param ]
	scheme, rest := parseToken(header)
	if scheme == "" {
		return
	}
	scheme = strings.ToLower(scheme)

	// fast path for non bearer challenge
	if scheme != "bearer" {
		return
	}

	// parse params for bearer auth.
	// combining RFC 7235 section 2.1 with RFC 7230 section 7, we have
	//     #auth-param => auth-param *( OWS "," OWS auth-param )
	var key, value, tail string
	for {
		key, rest = parseToken(skipSpace(rest))
		if key == "" {
			return
		}

		rest = skipSpace(rest)
		if rest == "" || rest[0] != '=' {
			return
		}
		rest = skipSpace(rest[1:])
		if rest == "" {
			return
		}

		if rest[0] == '"' {
			value, tail = parseQuotedString(rest)
			if rest == tail {
				return
			}
			rest = tail
		} else {
			value, rest = parseToken(rest)
			if value == "" {
				return
			}
		}
		if params == nil {
			params = map[string]string{
				key: value,
			}
		} else {
			params[key] = value
		}

		rest = skipSpace(rest)
		if rest == "" || rest[0] != ',' {
			return
		}
		rest = rest[1:]
	}
}

// isNotTokenChar reports whether rune is not a `tchar` defined in RFC 7230
// section 3.2.6.
func isNotTokenChar(r rune) bool {
	// tchar = "!" / "#" / "$" / "%" / "&" / "'" / "*"
	//       / "+" / "-" / "." / "^" / "_" / "`" / "|" / "~"
	//       / DIGIT / ALPHA
	//       ; any VCHAR, except delimiters
	switch {
	case r >= 'A' && r <= 'Z',
		r >= 'a' && r <= 'z',
		r >= '0' && r <= '9',
		strings.ContainsRune("!#$%&'*+-.^_`|~", r):
		return false
	}
	return true
}

// parseToken finds the next token from the given string. If no token found,
// an empty token is returned and the whole of the input is returned in rest.
// Note: Since token = 1*tchar, empty string is not a valid token.
func parseToken(s string) (token, rest string) {
	if i := strings.IndexFunc(s, isNotTokenChar); i != -1 {
		return s[:i], s[i:]
	}
	return s, ""
}

// skipSpace skips "bad" whitespace (BWS) defined in RFC 7230 section 3.2.3.
func skipSpace(s string) string {
	// OWS = *( SP / HTAB )
	//     ; optional whitespace
	// BWS = OWS
	//     ; "bad" whitespace
	if i := strings.IndexFunc(s, func(r rune) bool {
		return r != ' ' && r != '\t'
	}); i != -1 {
		return s[i:]
	}
	return s
}

// parseQuotedString finds the next quoted string from the given string.
// If no quoted string found, an empty string is returned and the whole of the
// input is returned in rest.
// Note: it is possible to have quoted empty string as "".
func parseQuotedString(s string) (value, rest string) {
	if s == "" || s[0] != '"' {
		return "", s
	}

	i := 1
	for {
		offset := strings.IndexByte(s[i:], '"')
		if offset == -1 {
			return "", s
		}
		i += offset
		offset = strings.LastIndexFunc(s[:i], func(r rune) bool {
			return r != '\\'
		})
		i++
		if offset == -1 || (i-offset)%2 == 0 {
			// no escaping for '"' found
			break
		}
	}
	var err error
	value, err = strconv.Unquote(s[:i])
	if err != nil {
		return "", s
	}
	rest = s[i:]
	return
}
