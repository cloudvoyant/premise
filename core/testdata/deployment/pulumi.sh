#!/bin/sh
printf 'pulumi release=%s %s\n' "$PM_RELEASE" "$*" >> "$TEST_DEPLOY_LOG"
if [ "$1" = "stack" ] && [ "$2" = "ls" ]; then
 printf '[{"name":"dev"},{"name":"stage"},{"name":"prod"},{"name":"%s"}]' "${TEST_PREVIEW_STACK:-preview-test}"
fi
if [ "$1" = "stack" ] && [ "$2" = "export" ]; then
 if [ "$TEST_INFRA_EXISTS" = "1" ]; then
  printf '{"deployment":{"resources":[{"type":"test:storage:Bucket"}]}}'
 else
  printf '{"deployment":{"resources":[]}}'
 fi
fi
if [ "$1" = "stack" ] && [ "$2" = "select" ]; then
 [ "$TEST_FAILURE_PHASE" != "init" ] || exit 1
fi
if [ "$1" = "up" ]; then
 [ "$TEST_FAILURE_PHASE" != "apply" ] || exit 1
fi
