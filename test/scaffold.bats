#!/usr/bin/env bats
# Tests for mise-tasks/scaffold
#
# Install bats: brew install bats-core
# Run: bats test/scaffold.bats

setup() {
    export ORIGINAL_DIR="$PWD"

    # Create temporary project directory with test name for easier debugging
    # BATS encodes special chars as -XX (hex), decode them using perl
    TEST_NAME_DECODED=$(printf '%s' "$BATS_TEST_NAME" | perl -pe 's/-([0-9a-f]{2})/chr(hex($1))/gie')
    TEST_NAME_SANITIZED=$(printf '%s' "$TEST_NAME_DECODED" | tr '[:upper:]' '[:lower:]' | sed 's/[^a-z0-9]/-/g' | sed 's/-\+/-/g')
    export PROJECT_DIR="$ORIGINAL_DIR/.tmp/$TEST_NAME_SANITIZED"
    mkdir -p "$PROJECT_DIR"

    # Clone template repo to project/.tmp/$PROJECT (simulating scripting CLI behavior)
    export TEMPLATE_CLONE="$PROJECT_DIR/.tmp/$PROJECT"
    mkdir -p "$TEMPLATE_CLONE"

    # Copy all files except .git and gitignored directories to template clone
    rsync -a \
        --exclude='.git' \
        --exclude='.tmp' \
        --exclude='node_modules' \
        "$ORIGINAL_DIR/" "$TEMPLATE_CLONE/"

    # Set up test variables
    export DEST_DIR="$PROJECT_DIR"
    export SRC_DIR="$TEMPLATE_CLONE"

    # Change to the template clone directory (where scaffold will be called from)
    cd "$TEMPLATE_CLONE"

    # Get VERSION from version.txt
    if [ -f "version.txt" ]; then
        VERSION=$(cat version.txt | tr -d '[:space:]')
        export VERSION
    fi
    # Get PROJECT from mise.toml
    if [ -f "mise.toml" ]; then
        PROJECT=$(grep '^PROJECT' mise.toml | head -1 | sed 's/PROJECT *= *"\(.*\)"/\1/')
        export PROJECT
    fi
}

teardown() {
    # Clean up test directories
    cd "$ORIGINAL_DIR"
    rm -rf "$PROJECT_DIR"
}

@test "scaffold.sh defaults to project root when --src and --dest not provided" {
    # When run without args, should use current directory as default
    # We'll run with --non-interactive to avoid prompts
    run bash ./mise-tasks/scaffold --non-interactive

    # Should succeed (defaults to current dir for both src and dest)
    [ "$status" -eq 0 ]
    [[ "$output" == *"Scaffolding complete"* ]]
}

@test "scaffold.sh validates source directory exists" {
    run bash ./mise-tasks/scaffold --src /nonexistent --dest ../..

    [ "$status" -eq 1 ]
    [[ "$output" == *"Source directory does not exist"* ]]
}

@test "scaffold.sh validates destination directory exists" {
    run bash ./mise-tasks/scaffold --src . --dest /nonexistent

    [ "$status" -eq 1 ]
    [[ "$output" == *"Destination directory does not exist"* ]]
}

@test "validates project name in non-interactive mode" {
    # Rejects invalid characters (spaces)
    run bash ./mise-tasks/scaffold \
        --src . \
        --dest ../.. \
        --non-interactive \
        --project "my project"

    [ "$status" -eq 1 ]
    [[ "$output" == *"Invalid project name"* ]]

    # Accepts valid characters
    run bash ./mise-tasks/scaffold \
        --src . \
        --dest ../.. \
        --non-interactive \
        --project "my-valid_project123"

    [ "$status" -eq 0 ]
    [[ "$output" == *"project=my-valid_project123"* ]]
}

@test "updates mise.toml with template tracking variables" {
    bash ./mise-tasks/scaffold \
        --src . \
        --dest ../.. \
        --non-interactive \
        --project testproject

    # Sets project name
    run grep 'PROJECT.*=.*"testproject"' "$DEST_DIR/mise.toml"
    [ "$status" -eq 0 ]

    # Adds template tracking (reads from source mise.toml)
    run grep 'TEMPLATE' "$DEST_DIR/mise.toml"
    [ "$status" -eq 0 ]
    [[ "$output" == *"mise-lib-template"* ]]

    run grep 'TEMPLATE_VERSION' "$DEST_DIR/mise.toml"
    [ "$status" -eq 0 ]
    [[ "$output" == *"$VERSION"* ]]

    # Resets project version to 0.0.0 in version.txt
    [ -f "$DEST_DIR/version.txt" ]
    run cat "$DEST_DIR/version.txt"
    [ "$status" -eq 0 ]
    [[ "$output" == "0.0.0" ]]

    # No duplicates on second run
    bash ./mise-tasks/scaffold \
        --src . \
        --dest ../.. \
        --non-interactive \
        --project testproject

    count=$(grep -c "^TEMPLATE" "$DEST_DIR/mise.toml")
    [ "$count" -eq 2 ]
}

@test "handles .claude directory with --keep-claude option" {
    mkdir -p "$DEST_DIR/.claude"
    touch "$DEST_DIR/.claude/plan.md" "$DEST_DIR/.claude/workflows.md" "$DEST_DIR/.claude/CLAUDE.md"

    # By default (without --keep-claude), removes entire .claude directory
    bash ./mise-tasks/scaffold \
        --src . \
        --dest ../.. \
        --non-interactive \
        --project testproject

    [ ! -d "$DEST_DIR/.claude" ]

    # With --keep-claude, keeps entire .claude directory
    bash ./mise-tasks/scaffold \
        --src . \
        --dest ../.. \
        --non-interactive \
        --project testproject \
        --keep-claude

    [ -d "$DEST_DIR/.claude" ]
    [ -f "$DEST_DIR/CLAUDE.md" ]
    [ -f "$DEST_DIR/.claude/workflows.md" ]
}


@test "removes platform-specific files from destination" {
    bash ./mise-tasks/scaffold \
        --src . \
        --dest ../.. \
        --non-interactive \
        --project testproject

    # Template development files should be removed
    [ ! -d "$DEST_DIR/test" ]
    [ ! -d "$DEST_DIR/templates" ]
    [ ! -f "$DEST_DIR/CHANGELOG.md" ]
    [ ! -f "$DEST_DIR/RELEASE_NOTES.md" ]

    # Template-only mise-tasks should be removed, including the one-shot scaffold entrypoint
    [ ! -f "$DEST_DIR/mise-tasks/scaffold" ]
    [ ! -d "$DEST_DIR/mise-tasks/templates" ]
    [ ! -f "$DEST_DIR/mise-tasks/template-utils" ]

    # Template section should be removed from mise.toml
    run grep "^# TEMPLATE$" "$DEST_DIR/mise.toml"
    [ "$status" -eq 1 ]

    # .envrc should NOT exist in destination
    [ ! -f "$DEST_DIR/.envrc" ]
}

@test "replaces README.md with template" {
    bash ./mise-tasks/scaffold \
        --src . \
        --dest ../.. \
        --non-interactive \
        --project myproject

    # README should exist
    [ -f "$DEST_DIR/README.md" ]

    # Should contain project name
    run grep "# myproject" "$DEST_DIR/README.md"
    [ "$status" -eq 0 ]

    # Should contain template name
    run grep "mise-lib-template" "$DEST_DIR/README.md"
    [ "$status" -eq 0 ]

    # Should contain platform version
    run grep "v$VERSION" "$DEST_DIR/README.md"
    [ "$status" -eq 0 ]

    # Should not contain template placeholders
    run grep "{{PROJECT_NAME}}" "$DEST_DIR/README.md"
    [ "$status" -eq 1 ]
}

@test "shows success message on completion" {
    run bash ./mise-tasks/scaffold \
        --src . \
        --dest ../.. \
        --non-interactive \
        --project myproject

    [ "$status" -eq 0 ]
    [[ "$output" == *"Scaffolding complete"* ]]
    [[ "$output" == *"Project: myproject"* ]]
}

@test "uses destination directory name as default project name" {
    # Create a properly named destination directory
    NEW_DEST="$ORIGINAL_DIR/.tmp/my-awesome-project"
    mkdir -p "$NEW_DEST"

    # Copy platform files to the new destination
    rsync -a \
        --exclude='.git' \
        --exclude='.tmp' \
        --exclude='node_modules' \
        . "$NEW_DEST/"

    run bash ./mise-tasks/scaffold \
        --src . \
        --dest "$NEW_DEST" \
        --non-interactive

    [ "$status" -eq 0 ]
    [[ "$output" == *"project=my-awesome-project"* ]]

    cd "$ORIGINAL_DIR"
    rm -rf "$NEW_DEST"
}

@test "restores original directory on failure" {
    # Destination starts empty (only .tmp directory from setup)
    INITIAL_FILE_COUNT=$(find "$DEST_DIR" -mindepth 1 -maxdepth 1 ! -name '.tmp' | wc -l)
    [ "$INITIAL_FILE_COUNT" -eq 0 ]

    # Make README.template.md unreadable to cause failure during template substitution
    chmod 000 "$SRC_DIR/README.template.md"

    # Try to run scaffold (should fail during README template substitution)
    run bash ./mise-tasks/scaffold \
        --src . \
        --dest ../.. \
        --non-interactive \
        --project testproject

    # Restore permissions
    chmod 644 "$SRC_DIR/README.template.md"

    # Should have failed
    [ "$status" -ne 0 ]
    [[ "$output" == *"Restoring original directory"* ]]

    # Should be restored to empty (only .tmp directory should exist)
    FILE_COUNT=$(find "$DEST_DIR" -mindepth 1 -maxdepth 1 ! -name '.tmp' | wc -l)
    [ "$FILE_COUNT" -eq 0 ]
}

@test "removes backup directory on success" {
    bash ./mise-tasks/scaffold \
        --src . \
        --dest ../.. \
        --non-interactive \
        --project testproject

    # Backup directory should not exist after successful scaffold
    [ ! -d "$DEST_DIR/.tmp/.scaffold-backup" ]
}

@test "replaces template name in all case variants across all files" {
    bash ./mise-tasks/scaffold \
        --src . \
        --dest ../.. \
        --non-interactive \
        --project my_awesome_project

    # Check PascalCase replacement in src files
    run grep "class MyAwesomeProject" "$DEST_DIR/src/sample-code.txt"
    [ "$status" -eq 0 ]

    run grep "MyAwesomeProjectService" "$DEST_DIR/src/sample-code.txt"
    [ "$status" -eq 0 ]

    # Check camelCase replacement in src files
    run grep "myAwesomeProjectConfig" "$DEST_DIR/src/sample-code.txt"
    [ "$status" -eq 0 ]

    run grep "myAwesomeProjectHelper" "$DEST_DIR/src/sample-code.txt"
    [ "$status" -eq 0 ]

    # Check README contains project name
    run grep "my_awesome_project" "$DEST_DIR/README.md"
    [ "$status" -eq 0 ]

    # Verify template name no longer appears in mise.toml PROJECT line
    run grep -r 'PROJECT.*=.*"mise-lib-template"' "$DEST_DIR" --exclude-dir=.tmp
    [ "$status" -eq 1 ]
}

@test "scaffolded project has correct mise.toml tasks" {
    bash ./mise-tasks/scaffold \
        --src . \
        --dest ../.. \
        --non-interactive \
        --project testproject

    cd "$DEST_DIR"

    # Should have upgrade task as file task
    [ -f "mise-tasks/upgrade" ]

    # Upgrade task should call claude /upgrade
    run grep -q 'claude /upgrade' "mise-tasks/upgrade"
    [ "$status" -eq 0 ]

    # Should NOT have template development tasks (in TEMPLATE section)
    run grep -q '^\[tasks.scaffold\]' mise.toml
    [ "$status" -eq 1 ]

    [ ! -d "mise-tasks/templates" ]

    # Should NOT have TEMPLATE section
    run grep -q "^# TEMPLATE$" mise.toml
    [ "$status" -eq 1 ]
}

@test "template source has development tasks as files" {
    cd "$SRC_DIR"

    # User-facing tasks (as file tasks)
    [ -f "mise-tasks/upgrade" ]

    # Template development tasks live under mise-tasks/templates/
    [ -f "mise-tasks/templates/test" ]
    [ -d "mise-tasks/templates" ]
}

@test "scaffold.sh processes install.sh.template when --non-interactive (defaults to no install.sh)" {
    # When run with --non-interactive, install.sh.template should be removed (default: no install.sh)
    run bash ./mise-tasks/scaffold --src "$SRC_DIR" --dest "$DEST_DIR" --non-interactive --project test-project

    [ "$status" -eq 0 ]

    # Destination should not have install.sh (not requested)
    [ ! -f "$DEST_DIR/install.sh" ]

    # Destination should not have install.sh.template (removed)
    [ ! -f "$DEST_DIR/install.sh.template" ]
}

@test "scaffold.sh with --keep-claude removes commands except upgrade.md" {
    # When run with --keep-claude, only upgrade.md and README.md should remain
    run bash ./mise-tasks/scaffold --src "$SRC_DIR" --dest "$DEST_DIR" --non-interactive --project test-project --keep-claude

    [ "$status" -eq 0 ]

    # Only upgrade.md and README.md should remain
    [ -f "$DEST_DIR/.claude/commands/upgrade.md" ]
    [ -f "$DEST_DIR/.claude/commands/README.md" ]

    # adapt.md should be removed (template-only)
    [ ! -f "$DEST_DIR/.claude/commands/adapt.md" ]

    # Plugin commands should not exist
    [ ! -f "$DEST_DIR/.claude/commands/plan.md" ]
    [ ! -f "$DEST_DIR/.claude/commands/commit.md" ]
    [ ! -f "$DEST_DIR/.claude/commands/review.md" ]
}

@test "scaffold without --template flag uses agnostic mode" {
    run bash "$SRC_DIR/mise-tasks/scaffold" \
        --src "$SRC_DIR" --dest "$DEST_DIR" \
        --project "my-lib" --non-interactive
    [ "$status" -eq 0 ]
    grep -q 'TEMPLATE.*"mise-lib-template"' "$DEST_DIR/mise.toml"
}

@test "in-place scaffold removes templates/ and template tests from pre-populated dest" {
    # Simulate the real CLI flow: platform files (incl. templates/ + test/) are already
    # present in DEST, then scaffold runs in-place (src == dest).
    INPLACE="$PROJECT_DIR/.tmp/inplace"
    mkdir -p "$INPLACE"
    rsync -a \
        --exclude='.git' \
        --exclude='.tmp' \
        --exclude='node_modules' \
        "$ORIGINAL_DIR/" "$INPLACE/"

    # Sanity: the dirs we expect to be cleaned exist before scaffolding
    [ -d "$INPLACE/templates" ]
    [ -f "$INPLACE/test/scaffold-templates.bats" ]

    run bash "$INPLACE/mise-tasks/scaffold" \
        --src "$INPLACE" \
        --dest "$INPLACE" \
        --non-interactive \
        --project inplaceproj
    [ "$status" -eq 0 ]

    # PL-5: templates/ and the template test files must be gone
    [ ! -d "$INPLACE/templates" ]
    [ ! -d "$INPLACE/test" ]
}

@test "scaffold replaces docs/architecture.md and docs/user-guide.md with placeholders" {
    bash ./mise-tasks/scaffold \
        --src . \
        --dest ../.. \
        --non-interactive \
        --project placeholderdocs

    # Both docs must exist
    [ -f "$DEST_DIR/docs/architecture.md" ]
    [ -f "$DEST_DIR/docs/user-guide.md" ]

    # Each must be the generated placeholder (title + Placeholder marker)
    run grep -q "^# Architecture" "$DEST_DIR/docs/architecture.md"
    [ "$status" -eq 0 ]
    run grep -q "Placeholder" "$DEST_DIR/docs/architecture.md"
    [ "$status" -eq 0 ]

    run grep -q "^# User Guide" "$DEST_DIR/docs/user-guide.md"
    [ "$status" -eq 0 ]
    run grep -q "Placeholder" "$DEST_DIR/docs/user-guide.md"
    [ "$status" -eq 0 ]

    # Placeholders reference the new project name
    run grep -q "placeholderdocs" "$DEST_DIR/docs/architecture.md"
    [ "$status" -eq 0 ]
    run grep -q "placeholderdocs" "$DEST_DIR/docs/user-guide.md"
    [ "$status" -eq 0 ]

    # Placeholder body actually landed (guards against an empty-file-past-the-title
    # regression where only the title/marker survive but the TODO scaffolding is lost)
    run grep -q "TODO:" "$DEST_DIR/docs/architecture.md"
    [ "$status" -eq 0 ]
    run grep -q "TODO:" "$DEST_DIR/docs/user-guide.md"
    [ "$status" -eq 0 ]

    # Template-only content must NOT leak into the scaffolded docs
    run grep -q "Choosing a Template" "$DEST_DIR/docs/user-guide.md"
    [ "$status" -eq 1 ]
    run grep -q "GCP_SA_KEY" "$DEST_DIR/docs/architecture.md"
    [ "$status" -eq 1 ]
    run grep -q "mise.jdx.dev" "$DEST_DIR/docs/user-guide.md"
    [ "$status" -eq 1 ]
}
