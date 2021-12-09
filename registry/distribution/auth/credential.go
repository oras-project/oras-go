package auth

// Credential contains authentication credentials used to access remote
// registries.
type Credential struct {
	// Username is the name of the user for the remote registry.
	Username string

	// Password is the secret associated with the username.
	Password string

	// IdentityToken is a bearer token to be sent to the authorization service
	// for fetching access tokens.
	// An identity token is often used as a registry refresh token.
	// Reference: https://docs.docker.com/registry/spec/auth/oauth/
	IdentityToken string

	// RegistryToken is a bearer token to be sent to the registry.
	// An registry token is often called a registry access token.
	// Reference: https://docs.docker.com/registry/spec/auth/token/
	RegistryToken string
}
