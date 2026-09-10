#!/bin/bash

set -euo pipefail

SCRIPT_DIR="$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)"

: "${HOME:?HOME must be set}"

for module in "$SCRIPT_DIR"/*/go.mod; do
    command_dir="${module%/go.mod}"
    command_name="${command_dir##*/}"

    if [[ "$(cd "$command_dir" && go list -f '{{.Name}}' .)" != "main" ]]; then
        continue
    fi

    printf 'updating %s\n' "$command_name"
    (
        cd "$command_dir"
        go build
        updatebin "$HOME/bin/$command_name" linux "./$command_name"
    )
done

"$SCRIPT_DIR/9misc/update.bash"
