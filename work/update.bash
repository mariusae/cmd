#!/bin/bash

set -euo pipefail

SCRIPT_DIR="$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)"
cd "$SCRIPT_DIR"

: "${HOME:?HOME must be set}"

BUILD_DIR="$(mktemp -d)"
INSTALL_PATH="$HOME/bin/work"
INSTALL_TMP="$HOME/bin/.work.update.$$"
cleanup() {
    rm -rf -- "$BUILD_DIR"
    rm -f -- "$INSTALL_TMP"
}
trap cleanup EXIT

BINARY="$BUILD_DIR/work"
ARCHIVE="$BUILD_DIR/work.tar.gz"

# Re-pin apex to its upstream head, so the build tracks apex rather than
# whatever was last committed here.
go get github.com/mariusae/apex/go@latest
APEX_VERSION="$(go list -m -f '{{.Version}}' github.com/mariusae/apex/go)"
# go get only appends to go.sum; drop the hashes of the versions it replaced.
awk -v version="$APEX_VERSION" \
    '$1 != "github.com/mariusae/apex/go" || $2 == version || $2 == version "/go.mod"' \
    go.sum >"$BUILD_DIR/go.sum"
mv -f -- "$BUILD_DIR/go.sum" go.sum
printf 'using github.com/mariusae/apex/go %s\n' "$APEX_VERSION" >&2

go build -trimpath -o "$BINARY" .
tar -czf "$ARCHIVE" -C "$BUILD_DIR" work

if ! UPLOAD_OUTPUT="$(jf upload "$ARCHIVE" 2>&1)"; then
    printf '%s\n' "$UPLOAD_OUTPUT" >&2
    exit 1
fi
printf '%s\n' "$UPLOAD_OUTPUT" >&2

HANDLE="$(printf '%s\n' "$UPLOAD_OUTPUT" | awk '
    /File available as / {
        for (field = 1; field < NF; field++) {
            if ($field == "as") {
                print $(field + 1)
                exit
            }
        }
    }
')"
if [[ -z "$HANDLE" ]]; then
    echo "Could not extract the uploaded artifact handle" >&2
    exit 1
fi

mkdir -p -- "$HOME/bin"
{
    printf '#!/usr/bin/env dotslash\n\n'
    jq -n \
        --arg handle "$HANDLE" \
        '{
            name: "work",
            oncall: "oncall+dont_call_meriksen@xmail.facebook.com",
            platforms: {
                linux: {
                    scheme: "everstore",
                    handle: $handle,
                    extract: {
                        decompress: "tar.gz",
                        path: "work"
                    }
                }
            }
        }'
} >"$INSTALL_TMP"
chmod 0755 "$INSTALL_TMP"
mv -f -- "$INSTALL_TMP" "$INSTALL_PATH"

printf 'installed %s\n' "$INSTALL_PATH"
