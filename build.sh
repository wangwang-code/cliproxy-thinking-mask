#!/usr/bin/env bash
#
# Build the cliproxy-thinking-mask plugin as a linux/amd64 C-ABI shared library
# for CLIProxyAPI. Run this on the linux/amd64 target (or inside a
# linux/amd64 builder, e.g. `docker run --platform linux/amd64 -v "$PWD":/src
# -w /src golang:1.26 bash build.sh`).
#
# Requires a Go toolchain (>= go 1.22) with cgo enabled and a C cross/native
# compiler for linux/amd64 (gcc). The produced cliproxy-thinking-mask.so is
# dropped into the CPA plugin directory (see README.md).
set -euo pipefail
cd "$(dirname "$0")"

rm -f cliproxy-thinking-mask.so cliproxy-thinking-mask.h

GOOS=linux GOARCH=amd64 CGO_ENABLED=1 \
    go build -buildmode=c-shared -o cliproxy-thinking-mask.so .

# The generated C header is only needed by C/Rust consumers, not by CPA.
rm -f cliproxy-thinking-mask.h

echo "built: $(pwd)/cliproxy-thinking-mask.so"
file cliproxy-thinking-mask.so
