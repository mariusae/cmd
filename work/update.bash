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
