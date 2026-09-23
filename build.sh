#!/usr/bin/env bash
# Builds pit for all release platforms next to this script (gitignored). The version comes from VERSION. Tests: ./run-tests.sh
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")"

if ! command -v go >/dev/null 2>&1; then
    echo "error: go not found on PATH" >&2
    exit 1
fi

VERSION=$(tr -d '[:space:]' < VERSION)
if [ -z "$VERSION" ]; then
    echo "error: VERSION is empty" >&2
    exit 1
fi

# GOOS/GOARCH, and the name the platform gets in the binary's file name
TARGETS=(
    "windows amd64 windows-amd64.exe"
    "windows arm64 windows-arm64.exe"
    "linux   amd64 linux-amd64"
    "linux   arm64 linux-arm64"
    "darwin  arm64 macos-arm64"
)

for target in "${TARGETS[@]}"; do
    read -r os arch name <<< "$target"
    echo "Building pit-$name..."
    GOOS=$os GOARCH=$arch CGO_ENABLED=0 go build -trimpath \
        -ldflags "-s -w -X main.version=$VERSION" -o "./pit-$name" ./cmd/pit
done

echo "Done: pit $VERSION"
