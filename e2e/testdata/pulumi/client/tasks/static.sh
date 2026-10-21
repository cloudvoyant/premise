#!/usr/bin/env bash
set -euo pipefail
operation="$1"
environment="$2"
args=(--env "$environment")
if [[ "$environment" == preview ]]; then args+=(--branch "$PM_BRANCH"); fi
bucket="$(pm infra query "${args[@]}" --output bucketName)"
case "$operation" in
    migrate)
        printf '%s:%s\n' "$environment" "$PM_RELEASE" | gcloud storage cp - "gs://$bucket/schema-version.txt"
        ;;
    deploy | rollback)
        references="$PM_ARTIFACTS"
        if [[ "$operation" == rollback ]]; then references="$PM_PREVIOUS_ARTIFACTS"; fi
        artifact="$(printf '%s' "$references" | python3 -c 'import json,sys; print(json.load(sys.stdin)["web"])')"
        gcloud storage cp "$artifact" "gs://$bucket/index.html"
        ;;
    health)
        gcloud storage cat "gs://$bucket/index.html" | grep -Fx "<h1>Release $PM_RELEASE</h1>"
        [[ "${PREMISE_TEST_FAIL_HEALTH:-}" != "$environment" ]]
        ;;
    cleanup)
        gcloud storage rm "gs://$bucket/index.html" "gs://$bucket/schema-version.txt"
        ;;
esac
