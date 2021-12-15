package auth

// EmptyCredential represents an empty credential.
var EmptyCredential Credential

// Credential contains authentication credentials used to access remote
// registries.
type Credential struct {
	// Username is the name of the user for the remote registry.
	Username string

	// Password is the secret associated with the username.
	Password string

	// RefreshToken is a bearer token to be sent to the authorization service
	// for fetching access tokens.
	// A refresh token is often referred as an identity token.
	// Reference: https://docs.docker.com/registry/spec/auth/oauth/
	RefreshToken string

	// AccessToken is a bearer token to be sent to the registry.
	// An access token is often referred as a registry token.
	// Reference: https://docs.docker.com/registry/spec/auth/token/
	AccessToken string
}
