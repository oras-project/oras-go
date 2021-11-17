package remotes

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
)

// NewAuthChallengeError is a function that returns a pointer to an AuthChallengeError
// An auth challenge error is handled by the Registry instance to gain access to resources
func NewAuthChallengeError(challenge string) *AuthChallengeError {
	return &AuthChallengeError{challenge: challenge}
}

var ErrAuthChallenge = errors.New("auth-challenge")

// AuthChallengeError is an opaque type returned when encountering a 401 Unauthorized from the registry
type AuthChallengeError struct {
	challenge string
	error
}

// Is is a function to check if this error is the same type as the other error
func (a AuthChallengeError) Is(target error) bool {
	return target == ErrAuthChallenge
}

// Unwrap is a function that returns the underlying error
func (a AuthChallengeError) Unwrap() error {
	return ErrAuthChallenge
}

// Challenge header examples...
// Www-Authenticate: Bearer realm="https://example.azurecr.io/oauth2/token",service="example.azurecr.io"
// Www-Authenticate: Bearer realm="https://example.azurecr.io/oauth2/token",service="example.azurecr.io",scope="repository:ubuntu:pull"
// Www-Authenticate: Bearer realm="https://auth.docker.io/token",service="registry.docker.io",scope="repository:samalba/my-app:pull,push"

// Parsing headers to format requests to OAuth2Providers
var parseBearerChallengeHeader = regexp.MustCompile(`(:?realm=[\\]?"(\w[:/.\w-]+)[\\]?")|(service=[\\]?"(\w[\w.-]+)[\\]?")|(?:scope=[\\]?"(\w+:([a-zA-Z0-9/-]*):([\w,]*))[\\]?")|(error=[\\]?"(\w+)[\\]?")`)

// ParseChallenge is a function that parses a challenge object
func (a AuthChallengeError) ParseChallenge() (realm, service, scope, namespace string, err error) {
	unencoded, err := url.PathUnescape(a.challenge)
	if err != nil {
		return "", "", "", "", err
	}

	results := parseBearerChallengeHeader.FindAllStringSubmatch(unencoded, -1)
	if len(results) <= 0 {
		return "", "", "", "", fmt.Errorf("invalid challenge")
	}

	realm = results[0][2]
	service = results[1][4]

	if len(results) > 2 {
		scope = results[2][5]
		namespace = results[2][6]
	}

	return realm, service, scope, namespace, nil
}
