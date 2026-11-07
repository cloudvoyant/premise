#!/bin/sh
set -eu
root="$PWD"
case "$PWD" in */libs/new) root="../.." ;; esac
test "$PWD" != "$LIVE_WORKSPACE"
test "$PWD" != "$LIVE_WORKSPACE/libs/new"
test "${PREMISE_TEMPLATE_TEST:-}" = 1
test -f "$root/shared/helper.txt"
test -f "$root/.cargo/config.toml"
test -f "$root/libs/bin/mise.toml"
if [ "$*" = "run install" ]; then
    printf 'validated dependency output\n' > "$root/future.lock"
fi
if [ "${FAIL_BUILD:-}" = 1 ] && [ "$*" = "run build" ]; then
    echo 'controlled candidate build failure' >&2
    exit 7
fi
