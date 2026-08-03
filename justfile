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

# ── Release ────────────────────────────────────────────────────────────────────

# Tag and push a new release (e.g.: just release v0.2.0)
release version:
    #!/usr/bin/env bash
    set -euo pipefail
    if ! [[ "{{version}}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
        echo "Error: version must match vX.X.X (e.g. v1.2.0), got: {{version}}"
        exit 1
    fi
    if [ -n "$(git status --porcelain)" ]; then
        echo "Working tree is dirty. Commit or stash changes first."
        exit 1
    fi
    git tag -a "{{version}}" -m "Release {{version}}"
    git push origin "{{version}}"
    echo "Tagged and pushed {{version}}"

# ── Dev ────────────────────────────────────────────────────────────────────────

# Run all examples (integration smoke test — every example must exit 0)
examples:
    #!/usr/bin/env bash
    set -euo pipefail
    failed=0
    for d in examples/*/; do
        echo "=== $d ==="
        if ! go run ./"$d"; then
            echo "FAILED: $d"
            failed=1
        fi
    done
    if [ "$failed" -eq 1 ]; then
        echo "One or more examples failed."
        exit 1
    fi
    echo "All examples passed."

# Clearing the Go Cahce
# -cache flag is used to clear the Go build cache
# -testcache flag is used to clear the Go test cache
# -modcache flag is used to clear the Go module cache
# -all flag is used to clear all caches plus fuzz cache
clear-build:
    go clean -cache

clear-test:
    go clean -testcache

clear-all:
    go clean -cache -testcache -modcache -fuzzcache

# ── Docs ───────────────────────────────────────────────────────────────────────

# Preview docs site locally (auto-reloads on file changes)
# Opens at http://localhost:8000
docs-preview:
    docker run --rm -it -p 8000:8000 -v "$(pwd)":/docs zensical/zensical

# Build docs site locally (outputs to site/)
docs-build:
    docker run --rm -v "$(pwd)":/docs zensical/zensical build --clean
