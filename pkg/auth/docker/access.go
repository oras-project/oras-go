package docker

import (
	"context"
	"errors"

	"oras.land/oras-go/pkg/remotes"
	"oras.land/oras-go/pkg/remotes/oauth"

	"oras.land/oras-go/pkg/auth"
)

func NewRegistryWithAccessProvider(host, namespace string, configPaths []string, options ...auth.LoginOption) (*remotes.Registry, error) {
	cl, err := NewClient(configPaths...)
	if err != nil {
		return nil, err
	}

	dcl, ok := cl.(*Client)
	if !ok {
		return nil, errors.New("invalid client type")
	}

	if len(dcl.configs) == 0 {
		return nil, errors.New("client is not logged in, it cannot provide access")
	}

	err = dcl.LoginWithOpts(options...)
	if err != nil {
		return nil, err
	}

	ap := &dockerAccessProvider{
		client: dcl,
	}

	return remotes.NewRegistry(host, namespace, ap), nil
}

type (
	dockerAccessProvider struct {
		client *Client
	}

	dockerAccess struct {
		realm     string
		service   string
		namespace string
		scope     string
		method    accessMethod
	}

	accessMethod struct {
		allowInsecure bool
		allowV1Auth   bool
	}
)

func (dap *dockerAccessProvider) CheckAccess(ctx context.Context, host, image, username string) (*remotes.AccessStatus, error) {
	_, _, err := dap.client.Credential(host)
	if err != nil {
		return nil, err
	}

	st := dap.client.primaryCredentialsStore(host)
	conf, err := st.Get(host)
	if err != nil {
		return nil, err
	}

	if conf.Username != username {
		return nil, errors.New("unrecognized user")
	}

	return &remotes.AccessStatus{
		Image:      image,
		UserKey:    username,
		TokenKey:   host,
		AccessRoot: "docker-credential-helper",
	}, nil
}

func (dap *dockerAccessProvider) RevokeAccess(ctx context.Context, host, username string) (*remotes.AccessStatus, error) {
	err := dap.client.Logout(ctx, host)
	if err != nil {
		return nil, err
	}

	return &remotes.AccessStatus{
		UserKey:  username,
		TokenKey: host,
	}, nil
}

func (dap *dockerAccessProvider) GetAccess(ctx context.Context, challenge *remotes.AuthChallengeError) (remotes.Access, error) {
	realm, service, scope, namespace, err := challenge.ParseChallenge()
	if err != nil {
		return nil, err
	}

	username, password, err := dap.client.Credential(service)
	if err != nil {
		return nil, err
	}

	da := &dockerAccess{
		realm:     realm,
		service:   service,
		scope:     scope,
		namespace: namespace,
	}

	access, err := da.resolveMethod(ctx, username, password)
	if err != nil {
		return nil, err
	}

	return access, nil
}

func (da *dockerAccess) resolveMethod(ctx context.Context, username, password string) (remotes.Access, error) {
	if da.method.allowInsecure || da.method.allowV1Auth {
		return nil, errors.New("not implemented")
	}

	tokenSource := oauth.NewBasicAuthTokenSource(ctx, da.realm, da.service, username, password, da.scope)
	if tokenSource == nil {
		return nil, errors.New("docker access provider: could not get a token")
	}

	return oauth.NewTokenSourceAccess(tokenSource), nil
}

var _ remotes.AccessProvider = (*dockerAccessProvider)(nil)
