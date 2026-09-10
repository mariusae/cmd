#!/usr/bin/env bash

set -euo pipefail

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
test_dir=$(mktemp -d)
trap 'rm -rf -- "$test_dir"' EXIT
quote_command=$script_dir/'"'
replay_command=$script_dir/'""'

fail() {
    printf 'FAIL: %s\n' "$*" >&2
    exit 1
}

assert_equal() {
    local want=$1
    local got=$2
    local description=$3
    if [[ $got != "$want" ]]; then
        printf 'FAIL: %s\nwant: %q\n got: %q\n' "$description" "$want" "$got" >&2
        exit 1
    fi
}

mkdir -p "$test_dir/bin"
printf '%s\n' '#!/usr/bin/env bash' "cat \"\$WINDOW_TEXT\"" >"$test_dir/bin/wintext"
chmod 0755 "$test_dir/bin/wintext"

cat >"$test_dir/window" <<'EOF'
host% echo first
host% ls thing
host% echo first
host% " ignored
host$ git status
not a prompt
EOF

path="$script_dir:$test_dir/bin:$PATH"
got=$(PATH="$path" WINDOW_TEXT="$test_dir/window" "$quote_command")
assert_equal $'\tgit status' "$got" '" prints the newest command'

got=$(PATH="$path" WINDOW_TEXT="$test_dir/window" "$quote_command" 'echo|ls')
assert_equal $'\tls thing\n\techo first' "$got" '" filters and deduplicates commands'

got=$(PATH="$path" WINDOW_TEXT="$test_dir/window" "$quote_command" nomatch)
assert_equal '' "$got" '" prints nothing when no command matches'

cat >"$test_dir/bin/rc-test" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$2"
EOF
chmod 0755 "$test_dir/bin/rc-test"

got=$(PATH="$path" RC="$test_dir/bin/rc-test" WINDOW_TEXT="$test_dir/window" \
    "$replay_command" git 2>"$test_dir/replayed")
assert_equal 'git status' "$got" '"" executes the newest match'
got=$(<"$test_dir/replayed")
assert_equal $'\tgit status' "$got" '"" reports the command it executes'

if PATH="$path" RC="$test_dir/bin/rc-test" WINDOW_TEXT="$test_dir/window" \
    "$replay_command" nomatch >"$test_dir/no-match.out" 2>"$test_dir/no-match.err"; then
    fail '"" succeeds when no command matches'
fi
got=$(<"$test_dir/no-match.err")
assert_equal 'no such command found' "$got" '"" explains an empty match'

mkdir -p "$test_dir/files/dir/sub"
: >"$test_dir/files/dir/file"
: >"$test_dir/files/dir/.hidden"
: >"$test_dir/files/dir/run"
chmod 0755 "$test_dir/files/dir/run"

got=$(cd "$test_dir/files" && "$script_dir/lf" dir)
assert_equal $'dir/file\ndir/run*\ndir/sub/' "$got" 'lf preserves the directory prefix'

got=$(cd "$test_dir/files" && "$script_dir/lf" -A dir)
[[ $got == *'dir/.hidden'* ]] || fail 'lf -A includes hidden entries'

got=$(cd "$test_dir/files" && "$script_dir/lf" -d dir)
assert_equal 'dir/' "$got" 'lf -d lists the directory itself'

mkdir -p "$test_dir/files/columns"
for name in a bb ccc dddd; do
    : >"$test_dir/files/columns/$name"
done
got=$(cd "$test_dir/files" && COLUMNS=10 PATH=/usr/bin:/bin \
    "$script_dir/lc" columns)
assert_equal $'a    ccc\nbb   dddd' "$got" 'lc lays out names in Plan 9 column order'

mkdir -p "$test_dir/update-bin" "$test_dir/update-home/bin"
cat >"$test_dir/update-bin/updatebin" <<'EOF'
#!/usr/bin/env bash
[[ ! -e $1 ]] || exit 2
printf '%s\n\n{}\n' '#!/usr/bin/env dotslash' >"$1"
EOF
chmod 0755 "$test_dir/update-bin/updatebin"
printf '%s\n' '#!/usr/bin/env bash' 'echo legacy' >"$test_dir/update-home/bin/lf"
HOME="$test_dir/update-home" PATH="$test_dir/update-bin:/usr/bin:/bin" \
    "$script_dir/update.bash" >"$test_dir/update.out"
got=$(head -n 1 "$test_dir/update-home/bin/lf")
assert_equal '#!/usr/bin/env dotslash' "$got" 'updater installs lf over a legacy script'
got=$(<"$test_dir/update-home/bin/lf.pre-dotslash")
assert_equal $'#!/usr/bin/env bash\necho legacy' "$got" 'updater preserves a legacy script'

echo 'PASS'
