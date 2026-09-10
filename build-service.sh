#!/usr/bin/env bash
# Build service/aab if it's missing or older than its Go sources. Called by the
# start scripts so editing service code can't leave you silently running a stale
# binary.
#
# If Go isn't installed, this falls back to the existing binary (erroring only when
# there is none), so the sidecar/production path keeps working without a Go
# toolchain.
set -euo pipefail

REPO="$(cd "$(dirname "$0")" && pwd)"
SERVICE="$REPO/service/aab"

# Same Go detection as install.sh: PATH first, then the local SDK location.
GO_BIN="$(command -v go || true)"
if [[ -z "$GO_BIN" && -x "$HOME/.local/go-sdk/go/bin/go" ]]; then
    GO_BIN="$HOME/.local/go-sdk/go/bin/go"
fi

if [[ -z "$GO_BIN" ]]; then
    if [[ -x "$SERVICE" ]]; then
        echo "build-service: Go not found; using existing $SERVICE" >&2
        exit 0
    fi
    echo "ERROR: $SERVICE missing and Go not found (PATH or ~/.local/go-sdk/go/bin)." >&2
    exit 1
fi

# Rebuild if the binary is absent or any source (.go / go.mod / go.sum) is newer than it.
needs_build=0
if [[ ! -x "$SERVICE" ]]; then
    needs_build=1
elif [[ -n "$(find "$REPO/service" \( -name '*.go' -o -name 'go.mod' -o -name 'go.sum' \) -newer "$SERVICE" -print -quit)" ]]; then
    needs_build=1
fi

if [[ "$needs_build" == 1 ]]; then
    echo "build-service: compiling aab with $GO_BIN ..." >&2
    ( cd "$REPO/service" && "$GO_BIN" build -o aab ./cmd/aab )
    echo "build-service: -> $SERVICE" >&2
else
    echo "build-service: aab is up to date." >&2
fi
