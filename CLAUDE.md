# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

ORAS Go is a Go library for managing OCI (Open Container Initiative) artifacts, providing unified APIs for pushing, pulling, and managing artifacts across OCI-compliant registries, local file systems, and in-memory stores. It's compliant with the OCI Image Format Specification and OCI Distribution Specification.

## Development Commands

### Testing
```bash
make test           # Run tests with race detection and coverage
go test ./...       # Run all tests without coverage
go test ./content   # Run tests for specific package
```

### Building and Validation
```bash
make check-encoding # Check for CRLF line endings
make fix-encoding   # Fix CRLF line endings
make vendor         # Update vendor directory
make clean          # Clean ignored files
```

### Coverage
```bash
make covhtml        # Open coverage report in browser (requires prior test run)
```

## Code Architecture

### Core Package Structure

- **Root package (`oras`)** - Main APIs for copying, packing, and content operations
- **`content/`** - Content management and storage interfaces
  - `content/file/` - File-based content storage
  - `content/memory/` - In-memory content storage  
  - `content/oci/` - OCI layout storage
- **`registry/`** - Registry interfaces and operations
  - `registry/remote/` - Remote registry client implementations
  - `registry/remote/auth/` - Authentication handling
  - `registry/remote/credentials/` - Credential management
- **`internal/`** - Internal packages not part of the public API
  - `internal/cas/` - Content-addressable storage utilities
  - `internal/syncutil/` - Synchronization utilities
  - `internal/platform/` - Platform-specific utilities

### Key Concepts

- **Targets** - Abstractions for different storage backends (registry, file system, memory)
- **Content Stores** - Manage content storage and retrieval with content-addressable storage
- **Descriptors** - OCI descriptors that identify and describe content
- **Copy Operations** - Core functionality for copying artifacts between different targets
- **Graph Operations** - Handle dependencies between artifacts and their manifests

### Testing Patterns

Tests follow Go conventions with `*_test.go` files. The codebase includes:
- Unit tests for individual packages
- Example tests demonstrating API usage
- Integration-style tests for end-to-end operations

### Go Version Support

The project supports the two latest Go versions (currently 1.23 and 1.24) following Go's security policy. The minimum Go version is specified in `go.mod`.

## Key Files for Understanding

- `content.go` - Core content operations and interfaces
- `copy.go` - Primary copy functionality between targets  
- `pack.go` - Artifact packing operations
- `registry/registry.go` - Registry interface definitions
- `registry/remote/repository.go` - Remote repository implementation