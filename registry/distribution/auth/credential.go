package auth

// Credential contains authentication credentials used to access remote
// registries.
type Credential struct {
	// Username is the name of the user for the remote registry.
	Username string

	// Password is the secret associated with the username.
	Password string

	// RefreshToken is a bearer token to be sent to the authorization service
	// for fetching access tokens.
	// A refresh token is also called an identity token.
	// Reference: https://docs.docker.com/registry/spec/auth/oauth/
	RefreshToken string

	// AccessToken is a bearer token to be sent to the registry.
	// An access token is also called a registry token.
	// Reference: https://docs.docker.com/registry/spec/auth/token/
	AccessToken string
}
