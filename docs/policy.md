# Policy Package

The `policy` package provides support for the `containers-policy.json` format used for OCI image signature verification policies.

## Overview

This package implements parsing, validation, and evaluation of container image policies as defined in the [containers-policy.json specification](https://man.archlinux.org/man/containers-policy.json.5.en).

## Features

- **Policy Management**: Load, save, and validate policy configurations
- **Multiple Transports**: Support for docker, oci, and other transport types
- **Policy Requirements**:
  - `insecureAcceptAnything` - Accept any image without verification
  - `reject` - Reject all images
  - `signedBy` - Require GPG signature verification (placeholder)
  - `sigstoreSigned` - Require sigstore signature verification (placeholder)
- **Scope-based Policies**: Define different policies for different image scopes
- **Identity Matching**: Support for various identity matching strategies

## Usage

### Creating a Policy

```go
import "oras.land/oras-go/v2/registry/remote/policy"

// Create a basic policy
p := &policy.Policy{
    Default: policy.PolicyRequirements{&policy.Reject{}},
    Transports: map[policy.TransportName]policy.TransportScopes{
        policy.TransportDocker: {
            "": policy.PolicyRequirements{&policy.InsecureAcceptAnything{}},
        },
    },
}
```

### Loading and Saving Policies

```go
// Load from default location
policy, err := policy.LoadDefaultPolicy()
if err != nil {
    log.Fatal(err)
}

// Load from specific path
policy, err := policy.LoadPolicy("/path/to/policy.json")
if err != nil {
    log.Fatal(err)
}

// Save policy
err = policy.SavePolicy(p, "/path/to/policy.json")
if err != nil {
    log.Fatal(err)
}
```

### Evaluating Policies

```go
import "context"

// Create an evaluator
evaluator, err := policy.NewEvaluator(p)
if err != nil {
    log.Fatal(err)
}

// Check if an image is allowed
image := policy.ImageReference{
    Transport: policy.TransportDocker,
    Scope:     "docker.io/library/nginx",
    Reference: "docker.io/library/nginx:latest",
}

allowed, err := evaluator.IsImageAllowed(context.Background(), image)
if err != nil {
    log.Fatal(err)
}

fmt.Printf("Image allowed: %v\n", allowed)
```

### Signature Verification Policies

```go
// GPG signature verification
signedByReq := &policy.PRSignedBy{
    KeyType: "GPGKeys",
    KeyPath: "/path/to/trusted-key.gpg",
    SignedIdentity: &policy.SignedIdentity{
        Type: policy.MatchRepository,
    },
}

// Sigstore signature verification
sigstoreReq := &policy.PRSigstoreSigned{
    KeyPath: "/path/to/cosign.pub",
    Fulcio: &policy.FulcioConfig{
        CAPath:       "/path/to/fulcio-ca.pem",
        OIDCIssuer:   "https://oauth2.sigstore.dev/auth",
        SubjectEmail: "user@example.com",
    },
    RekorPublicKeyPath: "/path/to/rekor.pub",
    SignedIdentity: &policy.SignedIdentity{
        Type: policy.MatchRepository,
    },
}
```

## Policy File Format

The policy.json file follows this structure:

```json
{
  "default": [
    {"type": "reject"}
  ],
  "transports": {
    "docker": {
      "": [
        {"type": "insecureAcceptAnything"}
      ],
      "docker.io/library/nginx": [
        {"type": "reject"}
      ]
    }
  }
}
```

## Default Policy Locations

The package checks for policy files in the following order:

1. `$HOME/.config/containers/policy.json` (user-specific)
2. `/etc/containers/policy.json` (system-wide)

## Supported Transports

- `docker` - Docker registries
- `atomic` - Atomic registries
- `containers-storage` - Local containers storage
- `dir` - Directory transport
- `docker-archive` - Docker archive files
- `docker-daemon` - Docker daemon
- `oci` - OCI layout
- `oci-archive` - OCI archive files
- `sif` - Singularity Image Format
- `tarball` - Tarball transport

## Identity Matching Types

- `matchExact` - Exact identity match
- `matchRepoDigestOrExact` - Repository digest or exact match
- `matchRepository` - Repository match
- `exactReference` - Exact reference match
- `exactRepository` - Exact repository match
- `remapIdentity` - Remap identity with prefix

## Limitations

- The `signedBy` and `sigstoreSigned` requirement types are currently placeholders
- Full signature verification requires integration with GPG and sigstore libraries
- Advanced identity matching strategies are defined but not fully implemented

## Testing

Run the tests with:

```bash
go test ./registry/remote/policy/...
```

## References

- [containers-policy.json specification](https://man.archlinux.org/man/containers-policy.json.5.en)
- [OCI Image Spec](https://github.com/opencontainers/image-spec)
- [OCI Distribution Spec](https://github.com/opencontainers/distribution-spec)
