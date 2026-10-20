#!/bin/sh
printf 'pulumi release=%s %s\n' "$PM_RELEASE" "$*" >> "$TEST_DEPLOY_LOG"
previous=""
for argument in "$@"; do
 if [ "$previous" = "--save-plan" ]; then printf '%s' '{"resourcePlans":{"urn:db":{"goal":{"custom":true,"type":"test:db"}}}}' > "$argument"; fi
 previous="$argument"
done
if [ "$1" = "stack" ] && [ "$2" = "select" ]; then
 [ "$TEST_FAILURE_PHASE" != "init" ] || exit 1
fi
if [ "$1" = "stack" ] && [ "$2" = "output" ]; then
 [ "$TEST_FAILURE_PHASE" != "record" ] || exit 1
 printf '{"deploymentId":"deployment-%s"}' "$PM_RELEASE"
fi
if [ "$1" = "up" ]; then
 case "$*" in
  *--target*) [ "$TEST_FAILURE_PHASE" != "provision" ] || exit 1;;
  *) [ "$TEST_FAILURE_PHASE" != "apply" ] || exit 1;;
 esac
fi
