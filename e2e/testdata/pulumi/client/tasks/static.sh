#!/usr/bin/env bash
set -euo pipefail
operation="$1"
environment="${2:-}"
if [[ -n "${PREMISE_TEST_EVENTS:-}" ]]; then
    printf '%s:%s:%s\n' "$operation" "$environment" "${PM_RELEASE:-A}" >> "$PREMISE_TEST_EVENTS"
fi
[[ "${PREMISE_TEST_FAIL_TASK:-}" != "$operation" ]]
case "$operation" in
    build|test|format:check|lint)
        test -s "releases/${PM_RELEASE:-A}/index.html"
        exit
        ;;
    release:build)
        cp "releases/${PM_RELEASE:-A}/index.html" "$PREMISE_ARTIFACT_DIR/index.html"
        printf '%s:%s\n' "$PREMISE_RELEASE_CHANNEL" "$RELEASE_VERSION" > "$PREMISE_ARTIFACT_DIR/version.txt"
        exit
        ;;
esac
args=(--env "$environment")
if [[ "$environment" == preview ]]; then args+=(--branch "$PM_BRANCH"); fi
bucket="$(pm infra query "${args[@]}" --output bucketName)"
case "$operation" in
    migrate)
        printf '%s:%s\n' "$environment" "$PM_RELEASE" | gcloud storage cp - "gs://$bucket/schema-version.txt"
        ;;
    deploy)
        gcloud storage cp "releases/$PM_RELEASE/index.html" "gs://$bucket/index.html"
        ;;
    rollback)
        # Generation IDs are unique, but creation time determines version order.
        previous="$(gcloud storage ls --all-versions --long "gs://$bucket/index.html" | awk '$3 ~ /^gs:/ {print $2, $3}' | sort -r | sed -n '2s/^[^ ]* //p')"
        [[ -n "$previous" ]]
        gcloud storage cp "$previous" "gs://$bucket/index.html"
        ;;
    health)
        gcloud storage cat "gs://$bucket/index.html" | grep -Fx "<h1>Release $PM_RELEASE</h1>"
        [[ "${PREMISE_TEST_FAIL_HEALTH:-}" != "$environment" ]]
        ;;
esac
