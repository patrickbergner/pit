#!/usr/bin/env bash
# Runs go vet and the test suite. Extra arguments go to go test, e.g.
#   ./run-tests.sh -v -run TestLFS
# The end-to-end tests in cmd/pit build throwaway monorepos and public repos as local file:// repositories; they need git, and git-lfs for
# the LFS tests (skipped otherwise).
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")"

if ! command -v go >/dev/null 2>&1; then
    echo "error: go not found on PATH" >&2
    exit 1
fi

unformatted=$(gofmt -l .)
if [ -n "$unformatted" ]; then
    echo "error: not gofmt'ed:" >&2
    echo "$unformatted" >&2
    exit 1
fi

go vet ./...
go test "$@" ./...
