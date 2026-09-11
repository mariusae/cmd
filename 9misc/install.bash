#!/usr/bin/env bash

set -euo pipefail

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
: "${HOME:?HOME must be set}"
install_dir="${INSTALL_DIR:-$HOME/bin}"

mkdir -p -- "$install_dir"

for command_name in '"' '""' mc lc lf; do
    install_path="$install_dir/$command_name"

    if [[ $script_dir/$command_name -ef $install_path ]]; then
        printf '%s is already installed from this directory\n' "$command_name"
        continue
    fi

    printf 'installing %s\n' "$command_name"
    staged="$install_dir/.$command_name.new.$$"
    trap 'rm -f -- "$staged"' EXIT
    cp -- "$script_dir/$command_name" "$staged"
    chmod 755 "$staged"
    # A rename within the install directory replaces a running command
    # atomically, rather than writing over the file in place.
    mv -f -- "$staged" "$install_path"
    trap - EXIT
done
