#!/usr/bin/env bats
# Tests for GitHub template export behavior
#
# Validates that export-ignore attributes in .gitattributes correctly
# exclude platform-specific files when using "Use this template" on GitHub.
#
# GitHub uses `git archive` to create templates, which respects export-ignore.
#
# Install bats: brew install bats-core
# Run: bats test/template-export.bats

setup() {
    export ORIGINAL_DIR="$PWD"

    # Create temporary directory for testing
    export TEST_DIR="$(mktemp -d)"
    export ARCHIVE_FILE="$TEST_DIR/template.tar"
    export EXTRACT_DIR="$TEST_DIR/extracted"
    export REPO_DIR="$TEST_DIR/repo"

    # Must be in a git repo for git archive to work
    # The test assumes we're running from the platform repo itself
    if [ ! -d ".git" ]; then
        skip "Must run from git repository root"
    fi

    # Create a temporary git repo with current working tree state
    # This allows testing against the current (uncommitted) state
    mkdir -p "$REPO_DIR"
    rsync -a \
        --exclude='.git' \
        --exclude='.tmp' \
        --exclude='node_modules' \
        "$ORIGINAL_DIR/" "$REPO_DIR/"
    cd "$REPO_DIR"
    git init -q
    # Disable background gc/maintenance so no async git process keeps .git busy
    # during teardown's rm -rf (avoids a flaky "Directory not empty" race on CI).
    git config gc.auto 0
    git config maintenance.auto false
    git config user.email "test@test.com"
    git config user.name "Test"
    git add -A
    git commit -q -m "test snapshot"
    cd "$ORIGINAL_DIR"
}

teardown() {
    # Clean up test directory. Never let a cleanup race fail the test: the temp
    # dir is disposable and the CI runner reclaims it regardless.
    cd "$ORIGINAL_DIR"
    chmod -R u+w "$TEST_DIR" 2>/dev/null || true
    rm -rf "$TEST_DIR" 2>/dev/null || true
}

@test "git archive command works" {
    run git -C "$REPO_DIR" archive --format=tar --output="$ARCHIVE_FILE" HEAD

    [ "$status" -eq 0 ]
    [ -f "$ARCHIVE_FILE" ]
}

@test "archive includes all required files and directories" {
    git -C "$REPO_DIR" archive --format=tar --output="$ARCHIVE_FILE" HEAD
    mkdir -p "$EXTRACT_DIR"
    tar -xf "$ARCHIVE_FILE" -C "$EXTRACT_DIR"

    # Verify essential platform files are present
    [ -f "$EXTRACT_DIR/README.md" ]
    [ -f "$EXTRACT_DIR/mise.toml" ]
    [ -f "$EXTRACT_DIR/mise-tasks/scaffold" ]
    [ -d "$EXTRACT_DIR/.claude" ]

    # Claude commands directory should contain README with plugin installation info
    [ -f "$EXTRACT_DIR/.claude/commands/README.md" ]
    # Individual command files are now provided via the Claudevoyant plugin
    # and should not be in the template

    # Root-level style guide should be included
    [ -f "$EXTRACT_DIR/CLAUDE.md" ]

    # docs/ should exist but not docs/migrations/
    [ -d "$EXTRACT_DIR/docs" ]
    [ ! -d "$EXTRACT_DIR/docs/migrations" ]

    # mise-tasks/ should exist with scaffold but not platform-install.sh
    [ -d "$EXTRACT_DIR/mise-tasks" ]
    [ -f "$EXTRACT_DIR/mise-tasks/scaffold" ]
    [ ! -f "$EXTRACT_DIR/scripts/platform-install.sh" ]
}

@test "validates all platform-specific files are excluded in one archive" {
    git -C "$REPO_DIR" archive --format=tar --output="$ARCHIVE_FILE" HEAD
    mkdir -p "$EXTRACT_DIR"
    tar -xf "$ARCHIVE_FILE" -C "$EXTRACT_DIR"

    # All platform development files should be excluded
    [ ! -d "$EXTRACT_DIR/test" ]
    [ ! -d "$EXTRACT_DIR/docs/migrations" ]
    [ ! -f "$EXTRACT_DIR/CHANGELOG.md" ]
    [ ! -f "$EXTRACT_DIR/RELEASE_NOTES.md" ]
    [ ! -f "$EXTRACT_DIR/scripts/platform-install.sh" ]
    [ ! -f "$EXTRACT_DIR/.envrc" ]
    [ ! -f "$EXTRACT_DIR/.envrc.template" ]

    # mise.toml SHOULD be in archive
    [ -f "$EXTRACT_DIR/mise.toml" ]

    # Platform-specific Claude files should be excluded
    [ ! -f "$EXTRACT_DIR/.claude/plan.md" ]
    [ ! -f "$EXTRACT_DIR/.claude/tasks.md" ]
    [ ! -f "$EXTRACT_DIR/.claude/migrations/generate-migration-guide.md" ]
    [ ! -f "$EXTRACT_DIR/.claude/commands/new-migration.md" ]
    [ ! -f "$EXTRACT_DIR/.claude/commands/validate-platform.md" ]

    # Template-specific commands should be included
    [ -f "$EXTRACT_DIR/.claude/commands/upgrade.md" ]
    [ -f "$EXTRACT_DIR/.claude/commands/adapt.md" ]
    [ -f "$EXTRACT_DIR/.claude/commands/README.md" ]

    # Plugin-provided commands should not be in template
    [ ! -f "$EXTRACT_DIR/.claude/commands/commit.md" ]
    [ ! -f "$EXTRACT_DIR/.claude/commands/plan.md" ]
    [ ! -f "$EXTRACT_DIR/.claude/commands/review.md" ]
    [ ! -f "$EXTRACT_DIR/.claude/commands/docs.md" ]
    [ ! -f "$EXTRACT_DIR/.claude/commands/adr-new.md" ]
    [ ! -f "$EXTRACT_DIR/.claude/commands/adr-capture.md" ]
}

@test "git archive includes templates/ directory" {
    run git -C "$REPO_DIR" archive HEAD --format=tar -- templates/
    [ "$status" -eq 0 ]
}

@test "git archive includes templates/uv/mise.toml" {
    local listing
    listing=$(git -C "$REPO_DIR" archive HEAD --format=tar | tar -t)
    echo "$listing" | grep -q "templates/uv/mise.toml"
}

@test "git archive includes templates/zig/mise.toml" {
    local listing
    listing=$(git -C "$REPO_DIR" archive HEAD --format=tar | tar -t)
    echo "$listing" | grep -q "templates/zig/mise.toml"
}

@test "git archive includes templates/go/mise.toml" {
    local listing
    listing=$(git -C "$REPO_DIR" archive HEAD --format=tar | tar -t)
    echo "$listing" | grep -q "templates/go/mise.toml"
}

@test "git archive includes templates/uv/CLAUDE.md.append" {
    local listing
    listing=$(git -C "$REPO_DIR" archive HEAD --format=tar | tar -t)
    echo "$listing" | grep -q "templates/uv/CLAUDE.md.append"
}

@test "git archive includes templates/zig/CLAUDE.md.append" {
    local listing
    listing=$(git -C "$REPO_DIR" archive HEAD --format=tar | tar -t)
    echo "$listing" | grep -q "templates/zig/CLAUDE.md.append"
}

@test "git archive includes templates/go/CLAUDE.md.append" {
    local listing
    listing=$(git -C "$REPO_DIR" archive HEAD --format=tar | tar -t)
    echo "$listing" | grep -q "templates/go/CLAUDE.md.append"
}

@test "templates/uv/CLAUDE.md.append not in export-ignore" {
    # Ensure .gitattributes does not accidentally exclude templates/
    run git -C "$REPO_DIR" check-attr export-ignore -- templates/uv/CLAUDE.md.append
    # Should either be unset or not export-ignore
    ! echo "$output" | grep -q "export-ignore: set"
}

@test "git archive includes templates/rust/mise.toml" {
    local listing
    listing=$(git -C "$REPO_DIR" archive HEAD --format=tar | tar -t)
    echo "$listing" | grep -q "templates/rust/mise.toml"
}

@test "git archive includes templates/rust/CLAUDE.md.append" {
    local listing
    listing=$(git -C "$REPO_DIR" archive HEAD --format=tar | tar -t)
    echo "$listing" | grep -q "templates/rust/CLAUDE.md.append"
}

@test "templates/rust/CLAUDE.md.append not in export-ignore" {
    run git -C "$REPO_DIR" check-attr export-ignore -- templates/rust/CLAUDE.md.append
    ! echo "$output" | grep -q "export-ignore: set"
}

@test "git archive includes templates/odin/mise.toml" {
    local listing
    listing=$(git -C "$REPO_DIR" archive HEAD --format=tar | tar -t)
    echo "$listing" | grep -q "templates/odin/mise.toml"
}

@test "git archive includes templates/odin/CLAUDE.md.append" {
    local listing
    listing=$(git -C "$REPO_DIR" archive HEAD --format=tar | tar -t)
    echo "$listing" | grep -q "templates/odin/CLAUDE.md.append"
}

@test "templates/odin/CLAUDE.md.append not in export-ignore" {
    run git -C "$REPO_DIR" check-attr export-ignore -- templates/odin/CLAUDE.md.append
    ! echo "$output" | grep -q "export-ignore: set"
}
