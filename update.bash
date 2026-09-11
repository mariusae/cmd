#!/bin/bash

set -euo pipefail

SCRIPT_DIR="$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)"

: "${HOME:?HOME must be set}"

APEX_MODULE="github.com/mariusae/apex/go"
UPDATE_TMP_DIR="$(mktemp -d)"
cleanup() {
    rm -rf -- "$UPDATE_TMP_DIR"
}
trap cleanup EXIT

update_apex() {
    local apex_version

    if ! go list -m "$APEX_MODULE" >/dev/null 2>&1; then
        return
    fi

    # Re-pin apex to its upstream head, so builds track apex rather than
    # whatever was last committed here.
    go get "$APEX_MODULE@latest"
    apex_version="$(go list -m -f '{{.Version}}' "$APEX_MODULE")"
    # go get only appends to go.sum; drop the hashes of the versions it replaced.
    awk -v module="$APEX_MODULE" -v version="$apex_version" \
        '$1 != module || $2 == version || $2 == version "/go.mod"' \
        go.sum >"$UPDATE_TMP_DIR/go.sum"
    mv -f -- "$UPDATE_TMP_DIR/go.sum" go.sum
    printf 'using %s %s\n' "$APEX_MODULE" "$apex_version" >&2
}

for module in "$SCRIPT_DIR"/*/go.mod; do
    command_dir="${module%/go.mod}"
    command_name="${command_dir##*/}"

    if [[ "$(cd "$command_dir" && go list -f '{{.Name}}' .)" != "main" ]]; then
        continue
    fi

    printf 'updating %s\n' "$command_name"
    (
        cd "$command_dir"
        update_apex
        go build
        updatebin "$HOME/bin/$command_name" linux "./$command_name"
    )
done

"$SCRIPT_DIR/9misc/update.bash"
