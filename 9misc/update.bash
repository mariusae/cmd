#!/usr/bin/env bash

set -euo pipefail

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
: "${HOME:?HOME must be set}"

for command_name in '"' '""' lc lf; do
    install_path="$HOME/bin/$command_name"
    legacy_path=

    if [[ -f $install_path ]] &&
        ! head -n 1 -- "$install_path" | grep -qx '#!/usr/bin/env dotslash'; then
        legacy_path="$install_path.pre-dotslash"
        if [[ -e $legacy_path || -L $legacy_path ]]; then
            printf 'cannot migrate %s: backup already exists at %s\n' \
                "$install_path" "$legacy_path" >&2
            exit 1
        fi
        mv -- "$install_path" "$legacy_path"
    fi

    printf 'updating %s\n' "$command_name"
    if ! updatebin "$install_path" linux "$script_dir/$command_name"; then
        if [[ -n $legacy_path ]]; then
            mv -f -- "$legacy_path" "$install_path"
        fi
        exit 1
    fi

    if [[ -n $legacy_path ]]; then
        printf 'preserved previous %s as %s\n' "$command_name" "$legacy_path"
    fi
done
