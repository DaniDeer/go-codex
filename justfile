# List all available recipes
default:
    @just --list

# ── Build ──────────────────────────────────────────────────────────────────────

# Build all packages
build:
    go build ./...

# Tidy module dependencies
tidy:
    go mod tidy

# ── Test & Quality ─────────────────────────────────────────────────────────────

# Run all tests
test:
    go test ./...

# Run all tests with verbose output
test-verbose:
    go test -v ./...

# Run tests with coverage report (opens HTML in browser)
cover:
    go test -coverprofile=coverage.out ./...
    go tool cover -html=coverage.out -o coverage.html
    @echo "Coverage report: coverage.html"

# List files with formatting issues
fmt:
    @gofmt -l .

# Run staticcheck static analysis (supersedes go vet)
staticcheck:
    staticcheck ./...

# Run gosec security scanner
gosec:
    gosec -conf gosec.config.json ./...

# Run fmt + staticcheck + gosec
check: staticcheck gosec
    #!/usr/bin/env bash
    files=$(gofmt -l .)
    if [ -n "$files" ]; then
        echo "Unformatted files:"
        echo "$files"
        exit 1
    fi
    echo "All checks passed."

# ── Dev ────────────────────────────────────────────────────────────────────────
