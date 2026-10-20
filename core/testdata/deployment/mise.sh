#!/bin/sh
if [ "$1" = exec ] && [ "$2" = -- ] && [ "$3" = pulumi ]; then shift 3; exec pulumi "$@"; fi
printf 'task %s release=%s previous=%s\n' "$2" "$PM_RELEASE" "$PM_PREVIOUS_RELEASE" >> "$TEST_DEPLOY_LOG"
failed_task="$TEST_FAILURE_PHASE"
[ "$failed_task" != "migration" ] || failed_task="migrate"
[ "$2" != "$failed_task" ] || exit 1
case "$2" in rollback|cleanup) [ "$TEST_RECOVERY_FAILURE" != "1" ] || exit 1;; esac
exit 0
