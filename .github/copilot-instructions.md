# Copilot Instructions

## Project Overview

ORAS Go: Go library for OCI artifact management. Provides unified APIs for push/pull operations across OCI registries, file systems, and memory stores. Compliant with OCI Image Format and Distribution Specifications.

**Stack:** Go (1.23+), OCI specifications

## Code Standards

- Follow Go idioms and best practices
- Use `gofmt` for formatting
- Run `make test` before commits (includes race detection + coverage)
- Maintain 80%+ test coverage for changes (required for CI to pass)
- Vendor dependencies via `make vendor`
- No CRLF line endings (`make check-encoding`)
- Apache 2.0 license header required on all source files (checked via `.github/workflows/license-checker.yml`)

## Repository Structure

```
oras-go/
├── content/           # Content management & storage
│   ├── file/         # File-based storage
│   ├── memory/       # In-memory storage
│   └── oci/          # OCI layout storage
├── registry/         # Registry interfaces & operations
│   └── remote/       # Remote registry client + auth
└── internal/         # Non-public utilities
    ├── cas/          # Content-addressable storage
    └── syncutil/     # Synchronization helpers
```

**Core files:** `content.go`, `copy.go`, `pack.go`, `registry/registry.go`

## Key Guidelines

1. **Public APIs:** Document all exported functions/types
2. **Testing:** Include unit tests for new functionality (`*_test.go`)
3. **Descriptors:** Use OCI descriptors for content identification
4. **Error handling:** Return descriptive errors with context
5. **Concurrency:** Use `internal/syncutil` patterns
6. **Go versions:** Support 2 latest releases (see `go.mod`)

## Common Tasks

```bash
make test      # Test with coverage
make covhtml   # View coverage report
go test ./...  # Run all tests
```

## Patterns

- **Targets:** Abstract storage backends (registry/file/memory)
- **Copy operations:** Primary API for artifact transfer
- **Content stores:** Content-addressable storage pattern
- **Graph operations:** Handle artifact dependencies
