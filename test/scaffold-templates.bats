#!/usr/bin/env bats
# test/scaffold-templates.bats
# Parameterized tests for --template [uv|zig] and agnostic (no template).
# Run with: bats test/scaffold-templates.bats

load 'helpers/contract'

TEMPLATE_SRC="$(cd "$(dirname "$BATS_TEST_FILENAME")/.." && pwd)"

setup() {
    DEST="$(mktemp -d)"
}

teardown() {
    rm -rf "$DEST"
}

# Helper: run scaffold for a given template (or agnostic if empty)
run_scaffold() {
    local template="${1:-}"
    local project="${2:-my-lib}"
    local github_org="${3:-}"
    local args=(
        --src "$TEMPLATE_SRC"
        --dest "$DEST"
        --project "$project"
        --non-interactive
    )
    [[ -n "$template" ]] && args+=(--template "$template")
    [[ -n "$github_org" ]] && args+=(--github-org "$github_org")
    bash "$TEMPLATE_SRC/mise-tasks/scaffold" "${args[@]}"
}

# ── Agnostic (no template) ────────────────────────────────────────────────────

@test "agnostic: scaffold succeeds" {
    run run_scaffold ""
    [ "$status" -eq 0 ]
}

@test "agnostic: honors base task contract" {
    # The agnostic base provides the infrastructure tasks; language tasks (lint, format, etc.)
    # are intentionally left as stubs for the user to fill in. Test only guaranteed tasks.
    run_scaffold ""
    local base_tasks=("build" "test" "upversion" "version" "version:next")
    for task in "${base_tasks[@]}"; do
        _task_exists "$DEST" "$task" || { echo "FAIL: base task '$task' missing"; false; }
    done
}

@test "agnostic: contract tasks are runnable" {
    run_scaffold ""
    assert_tasks_runnable "$DEST"
}

@test "agnostic: TEMPLATE is mise-lib-template" {
    run_scaffold ""
    grep -q 'TEMPLATE.*"mise-lib-template"' "$DEST/mise.toml"
}

@test "agnostic: src/sample-code.txt retained" {
    run_scaffold ""
    [ -f "$DEST/src/sample-code.txt" ]
}

@test "agnostic: no pyproject.toml created" {
    run_scaffold ""
    [ ! -f "$DEST/pyproject.toml" ]
}

@test "agnostic: no build.zig created" {
    run_scaffold ""
    [ ! -f "$DEST/build.zig" ]
}

# ── uv template ───────────────────────────────────────────────────────────────

@test "uv: scaffold succeeds" {
    run run_scaffold "uv"
    [ "$status" -eq 0 ]
}

@test "uv: honors full task contract" {
    run_scaffold "uv"
    assert_contract_tasks "$DEST"
}

@test "uv: contract tasks are runnable" {
    run_scaffold "uv"
    assert_tasks_runnable "$DEST"
}

@test "uv: pyproject.toml created with project name" {
    run_scaffold "uv" "my-lib"
    [ -f "$DEST/pyproject.toml" ]
    grep -q 'name = "my-lib"' "$DEST/pyproject.toml"
}

@test "uv: Python package directory created with project snake_case name" {
    run_scaffold "uv" "my-lib"
    [ -d "$DEST/src/my_lib" ]
    [ -f "$DEST/src/my_lib/__init__.py" ]
    [ -f "$DEST/src/my_lib/sample.py" ]
}

@test "uv: tests/ directory created" {
    run_scaffold "uv"
    [ -d "$DEST/tests" ]
    [ -f "$DEST/tests/test_sample.py" ]
}

@test "uv: TEMPLATE is mise-uv-template" {
    run_scaffold "uv"
    grep -q 'TEMPLATE.*"mise-uv-template"' "$DEST/mise.toml"
}

@test "uv: CLAUDE.md contains uv/ruff conventions" {
    run_scaffold "uv"
    grep -q "uv run" "$DEST/CLAUDE.md"
    grep -q "ruff" "$DEST/CLAUDE.md"
}

@test "uv: sample-code.txt removed" {
    run_scaffold "uv"
    [ ! -f "$DEST/src/sample-code.txt" ]
}

@test "uv: mise-tasks/ scripts are executable" {
    run_scaffold "uv"
    [ -x "$DEST/mise-tasks/publish/rc" ]
}

@test "uv: no build.zig created" {
    run_scaffold "uv"
    [ ! -f "$DEST/build.zig" ]
}

@test "uv: project name replaced in pyproject.toml script entry" {
    run_scaffold "uv" "cool-tool"
    grep -q 'cool-tool' "$DEST/pyproject.toml"
    grep -q 'cool_tool' "$DEST/pyproject.toml"
}

# ── zig template ──────────────────────────────────────────────────────────────

@test "zig: scaffold succeeds" {
    run run_scaffold "zig"
    [ "$status" -eq 0 ]
}

@test "zig: honors full task contract" {
    run_scaffold "zig"
    assert_contract_tasks "$DEST"
}

@test "zig: contract tasks are runnable" {
    run_scaffold "zig"
    assert_tasks_runnable "$DEST"
}

@test "zig: build.zig created" {
    run_scaffold "zig"
    [ -f "$DEST/build.zig" ]
}

@test "zig: build.zig.zon created with project name" {
    run_scaffold "zig" "my-lib"
    [ -f "$DEST/build.zig.zon" ]
    grep -q '.my_lib' "$DEST/build.zig.zon"
}

@test "zig: src/lib.zig and src/main.zig created" {
    run_scaffold "zig"
    [ -f "$DEST/src/lib.zig" ]
    [ -f "$DEST/src/main.zig" ]
}

@test "zig: TEMPLATE is mise-zig-template" {
    run_scaffold "zig"
    grep -q 'TEMPLATE.*"mise-zig-template"' "$DEST/mise.toml"
}

@test "zig: CLAUDE.md contains Zig conventions" {
    run_scaffold "zig"
    grep -q "zig build" "$DEST/CLAUDE.md"
    grep -q "zig fmt" "$DEST/CLAUDE.md"
}

@test "zig: sample-code.txt removed" {
    run_scaffold "zig"
    [ ! -f "$DEST/src/sample-code.txt" ]
}

@test "zig: mise-tasks/ scripts are executable" {
    run_scaffold "zig"
    [ -x "$DEST/mise-tasks/publish/_default" ]
    [ -x "$DEST/mise-tasks/build/all-platforms" ]
}

@test "zig: no pyproject.toml created" {
    run_scaffold "zig"
    [ ! -f "$DEST/pyproject.toml" ]
}

@test "zig: project name replaced in src/lib.zig" {
    run_scaffold "zig" "my-lib"
    grep -q 'my_lib' "$DEST/src/lib.zig"
}

# ── go template ───────────────────────────────────────────────────────────────

@test "go: scaffold succeeds" {
    run run_scaffold "go"
    [ "$status" -eq 0 ]
}

@test "go: honors full task contract" {
    run_scaffold "go"
    assert_contract_tasks "$DEST"
}

@test "go: contract tasks are runnable" {
    run_scaffold "go"
    assert_tasks_runnable "$DEST"
}

@test "go: go.mod created with project module path" {
    run_scaffold "go" "my-lib"
    [ -f "$DEST/go.mod" ]
    grep -q 'my-lib' "$DEST/go.mod"
}

@test "go: go.mod uses github org, not duplicated project name" {
    # Default org fallback (no git remote in the bats temp dest) is your-github-org.
    run_scaffold "go" "my-lib"
    grep -q '^module github.com/your-github-org/my-lib$' "$DEST/go.mod"
    # Regression guard: org segment must not collapse to the project name.
    ! grep -q 'github.com/my-lib/my-lib' "$DEST/go.mod"
}

@test "go: --github-org sets module path org segment" {
    run_scaffold "go" "my-lib" "acme-corp"
    grep -q '^module github.com/acme-corp/my-lib$' "$DEST/go.mod"
    grep -q 'github.com/acme-corp/my-lib/src/mylib' "$DEST/main.go"
}

@test "go: org rewrite leaves unrelated cloudvoyant links intact" {
    # The org rewrite must be scoped to the project's repo path, not blanket-replace
    # every github.com/cloudvoyant/* link (e.g. the claudevoyant plugin marketplace).
    run_scaffold "go" "my-lib" "acme-corp"
    grep -q 'cloudvoyant/claudevoyant' "$DEST/CONTRIBUTING.md"
    ! grep -rq 'acme-corp/claudevoyant' "$DEST"
}

@test "go: main.go and src lib package created" {
    run_scaffold "go" "my-lib"
    [ -f "$DEST/main.go" ]
    # Go packages use flatcase (no underscores): my-lib → mylib
    [ -d "$DEST/src/mylib" ]
    [ -f "$DEST/src/mylib/lib.go" ]
    [ -f "$DEST/src/mylib/lib_test.go" ]
}

@test "go: package name is flatcase, not snake_case" {
    run_scaffold "go" "my-lib"
    [ ! -d "$DEST/src/my_lib" ]
    grep -q '^package mylib$' "$DEST/src/mylib/lib.go"
}

@test "go: TEMPLATE is mise-go-template" {
    run_scaffold "go"
    grep -q 'TEMPLATE.*"mise-go-template"' "$DEST/mise.toml"
}

@test "go: CLAUDE.md contains Go conventions" {
    run_scaffold "go"
    grep -q "go build" "$DEST/CLAUDE.md"
    grep -q "go test" "$DEST/CLAUDE.md"
}

@test "go: sample-code.txt removed" {
    run_scaffold "go"
    [ ! -f "$DEST/src/sample-code.txt" ]
}

@test "go: mise-tasks/ scripts are executable" {
    run_scaffold "go"
    [ -x "$DEST/mise-tasks/publish/_default" ]
    [ -x "$DEST/mise-tasks/build/all-platforms" ]
}

@test "go: no pyproject.toml / build.zig / Cargo.toml created" {
    run_scaffold "go"
    [ ! -f "$DEST/pyproject.toml" ]
    [ ! -f "$DEST/build.zig" ]
    [ ! -f "$DEST/Cargo.toml" ]
}

@test "go: project name replaced in lib source" {
    run_scaffold "go" "my-lib"
    grep -q 'mylib' "$DEST/src/mylib/lib.go"
}

# ── pnpm template ─────────────────────────────────────────────────────────────

@test "pnpm: scaffold succeeds" {
    run run_scaffold "pnpm"
    [ "$status" -eq 0 ]
}

@test "pnpm: honors full task contract" {
    run_scaffold "pnpm"
    assert_contract_tasks "$DEST"
}

@test "pnpm: contract tasks are runnable" {
    run_scaffold "pnpm"
    assert_tasks_runnable "$DEST"
}

@test "pnpm: package.json created with project name" {
    run_scaffold "pnpm" "my-lib"
    [ -f "$DEST/package.json" ]
    grep -q '"name": "my-lib"' "$DEST/package.json"
}

@test "pnpm: src/index.ts and src/lib.ts created" {
    run_scaffold "pnpm"
    [ -f "$DEST/src/index.ts" ]
    [ -f "$DEST/src/lib.ts" ]
}

@test "pnpm: tests/lib.test.ts created" {
    run_scaffold "pnpm"
    [ -f "$DEST/tests/lib.test.ts" ]
}

@test "pnpm: TEMPLATE is mise-pnpm-template" {
    run_scaffold "pnpm"
    grep -q 'TEMPLATE.*"mise-pnpm-template"' "$DEST/mise.toml"
}

@test "pnpm: CLAUDE.md contains pnpm conventions" {
    run_scaffold "pnpm"
    grep -q "pnpm" "$DEST/CLAUDE.md"
}

@test "pnpm: sample-code.txt removed" {
    run_scaffold "pnpm"
    [ ! -f "$DEST/src/sample-code.txt" ]
}

@test "pnpm: no pyproject.toml created" {
    run_scaffold "pnpm"
    [ ! -f "$DEST/pyproject.toml" ]
}

@test "pnpm: no build.zig created" {
    run_scaffold "pnpm"
    [ ! -f "$DEST/build.zig" ]
}

@test "pnpm: mise-tasks/publish-rc is executable" {
    run_scaffold "pnpm"
    [ -x "$DEST/mise-tasks/publish/rc" ]
}

@test "pnpm: ci.yml has publish-rc job calling publish-rc task" {
    run_scaffold "pnpm"
    grep -q 'mise run publish:rc' "$DEST/.github/workflows/ci.yml"
}

@test "pnpm: ci.yml has no template-only tasks" {
    run_scaffold "pnpm"
    ! grep -q 'templates:' "$DEST/.github/workflows/ci.yml"
}

@test "pnpm: scaffolded project passes lint (ESLint + tsc)" {
    run_scaffold "pnpm"
    mise trust --yes "$DEST" >/dev/null 2>&1
    mise run --cd "$DEST" lint
}

@test "pnpm: scaffolded project passes format-check" {
    run_scaffold "pnpm"
    mise trust --yes "$DEST" >/dev/null 2>&1
    mise run --cd "$DEST" format:check
}

# ── CI workflow cleanup ───────────────────────────────────────────────────────

@test "agnostic: ci.yml has publish-rc job calling publish-rc task" {
    run_scaffold ""
    grep -q 'mise run publish:rc' "$DEST/.github/workflows/ci.yml"
}

@test "agnostic: ci.yml has no template-only tasks" {
    run_scaffold ""
    ! grep -q 'templates:' "$DEST/.github/workflows/ci.yml"
}

@test "agnostic: release.yml has no template-only tasks" {
    run_scaffold ""
    ! grep -q 'templates:' "$DEST/.github/workflows/release.yml"
}

@test "uv: ci.yml has publish-rc job calling publish-rc task" {
    run_scaffold "uv"
    grep -q 'mise run publish:rc' "$DEST/.github/workflows/ci.yml"
}

@test "uv: ci.yml has no template-only tasks" {
    run_scaffold "uv"
    ! grep -q 'templates:' "$DEST/.github/workflows/ci.yml"
}

@test "zig: ci.yml has publish-rc job calling publish-rc task" {
    run_scaffold "zig"
    grep -q 'mise run publish:rc' "$DEST/.github/workflows/ci.yml"
}

@test "zig: ci.yml has no template-only tasks" {
    run_scaffold "zig"
    ! grep -q 'templates:' "$DEST/.github/workflows/ci.yml"
}

@test "go: ci.yml has publish-rc job calling publish-rc task" {
    run_scaffold "go"
    grep -q 'mise run publish:rc' "$DEST/.github/workflows/ci.yml"
}

@test "go: ci.yml has no template-only tasks" {
    run_scaffold "go"
    ! grep -q 'templates:' "$DEST/.github/workflows/ci.yml"
}

@test "uv: mise-tasks/publish-rc is executable" {
    run_scaffold "uv"
    [ -x "$DEST/mise-tasks/publish/rc" ]
}

@test "zig: mise-tasks/publish-rc is executable" {
    run_scaffold "zig"
    [ -x "$DEST/mise-tasks/publish/rc" ]
}

@test "go: mise-tasks/publish-rc is executable" {
    run_scaffold "go"
    [ -x "$DEST/mise-tasks/publish/rc" ]
}

# ── Invalid template ──────────────────────────────────────────────────────────

@test "invalid template name exits with error" {
    run bash "$TEMPLATE_SRC/mise-tasks/scaffold" \
        --src "$TEMPLATE_SRC" --dest "$DEST" \
        --project "my-lib" --template python --non-interactive
    [ "$status" -ne 0 ]
    echo "$output" | grep -q -i "unknown template\|valid"
}

# ── Override verification ──────────────────────────────────────────────────────

@test "pnpm: nested task files are the template versions" {
    run_scaffold "pnpm"
    grep -q 'pnpm run build' "$DEST/mise-tasks/build/_default"
    grep -q 'pnpm publish'   "$DEST/mise-tasks/publish/_default"
}

@test "uv: nested task files are the template versions" {
    run_scaffold "uv"
    grep -q 'uv build' "$DEST/mise-tasks/build/_default"
}

@test "zig: nested override replaced base (publish)" {
    run_scaffold "zig"
    grep -q 'build:all-platforms' "$DEST/mise-tasks/publish/_default"
}

@test "go: nested override replaced base (build + publish)" {
    run_scaffold "go"
    grep -q 'go build ./...'       "$DEST/mise-tasks/build/_default"
    grep -q 'build:all-platforms'  "$DEST/mise-tasks/publish/_default"
    grep -q 'gh release upload'    "$DEST/mise-tasks/publish/_default"
    ! grep -q 'gcloud'             "$DEST/mise-tasks/publish/_default"
}

@test "agnostic: all task files executable (incl. nested)" {
    run_scaffold ""
    assert_tasks_executable "$DEST"
}

@test "uv: all task files executable (incl. nested)" {
    run_scaffold "uv"
    assert_tasks_executable "$DEST"
}

@test "zig: all task files executable (incl. nested)" {
    run_scaffold "zig"
    assert_tasks_executable "$DEST"
}

@test "pnpm: all task files executable (incl. nested)" {
    run_scaffold "pnpm"
    assert_tasks_executable "$DEST"
}

@test "go: all task files executable (incl. nested)" {
    run_scaffold "go"
    assert_tasks_executable "$DEST"
}

# ── rust template ─────────────────────────────────────────────────────────────

@test "rust: scaffold succeeds" {
    run run_scaffold "rust"
    [ "$status" -eq 0 ]
}

@test "rust: honors full task contract" {
    run_scaffold "rust"
    assert_contract_tasks "$DEST"
}

@test "rust: contract tasks are runnable" {
    run_scaffold "rust"
    assert_tasks_runnable "$DEST"
}

@test "rust: Cargo.toml created with project name" {
    run_scaffold "rust" "my-lib"
    [ -f "$DEST/Cargo.toml" ]
    grep -q 'my_lib' "$DEST/Cargo.toml"
}

@test "rust: Cargo.lock created" {
    run_scaffold "rust"
    [ -f "$DEST/Cargo.lock" ]
}

@test "rust: src/lib.rs and src/main.rs created" {
    run_scaffold "rust"
    [ -f "$DEST/src/lib.rs" ]
    [ -f "$DEST/src/main.rs" ]
}

@test "rust: TEMPLATE is mise-rust-template" {
    run_scaffold "rust"
    grep -q 'TEMPLATE.*"mise-rust-template"' "$DEST/mise.toml"
}

@test "rust: CLAUDE.md contains Rust conventions" {
    run_scaffold "rust"
    grep -q "cargo build" "$DEST/CLAUDE.md"
    grep -q "cargo clippy" "$DEST/CLAUDE.md"
}

@test "rust: sample-code.txt removed" {
    run_scaffold "rust"
    [ ! -f "$DEST/src/sample-code.txt" ]
}

@test "rust: mise-tasks/ scripts are executable" {
    run_scaffold "rust"
    [ -x "$DEST/mise-tasks/publish/_default" ]
    [ -x "$DEST/mise-tasks/build/all-platforms" ]
}

@test "rust: no pyproject.toml created" {
    run_scaffold "rust"
    [ ! -f "$DEST/pyproject.toml" ]
}

@test "rust: no build.zig.zon created" {
    run_scaffold "rust"
    [ ! -f "$DEST/build.zig.zon" ]
}

@test "rust: project name replaced in src/lib.rs" {
    run_scaffold "rust" "my-lib"
    grep -q 'mise-rust-template' "$DEST/src/lib.rs"
}

@test "rust: project name replaced in src/main.rs" {
    run_scaffold "rust" "my-lib"
    grep -q 'my_lib' "$DEST/src/main.rs"
}

@test "rust: ci.yml has publish-rc job calling publish-rc task" {
    run_scaffold "rust"
    grep -q 'mise run publish:rc' "$DEST/.github/workflows/ci.yml"
}

@test "rust: ci.yml has no template-only tasks" {
    run_scaffold "rust"
    ! grep -q 'templates:' "$DEST/.github/workflows/ci.yml"
}

@test "rust: mise-tasks/publish-rc is executable" {
    run_scaffold "rust"
    [ -x "$DEST/mise-tasks/publish/rc" ]
}

@test "rust: nested override replaced base (build + publish)" {
    run_scaffold "rust"
    grep -q 'cargo build'         "$DEST/mise-tasks/build/_default"
    grep -q 'build:all-platforms' "$DEST/mise-tasks/publish/_default"
}

@test "rust: nested task files are the template versions" {
    run_scaffold "rust"
    grep -q 'cargo test' "$DEST/mise-tasks/test"
    grep -q 'cargo clippy' "$DEST/mise-tasks/lint/_default"
}

@test "rust: all task files executable (incl. nested)" {
    run_scaffold "rust"
    assert_tasks_executable "$DEST"
}

# ── odin template ──────────────────────────────────────────────────────────────

@test "odin: scaffold succeeds" {
    run run_scaffold "odin"
    [ "$status" -eq 0 ]
}

@test "odin: honors full task contract" {
    run_scaffold "odin"
    assert_contract_tasks "$DEST"
}

# NOTE: assert_tasks_runnable runs build+test, which for odin actually compile via clang.
# Skip if clang is absent (local dev / CI without clang pre-installed).
@test "odin: contract tasks are runnable" {
    command -v clang >/dev/null 2>&1 || skip "clang/LLVM required for odin build/test"
    run_scaffold "odin"
    assert_tasks_runnable "$DEST"
}

@test "odin: src/lib.odin and src/main.odin created" {
    run_scaffold "odin"
    [ -f "$DEST/src/lib.odin" ]
    [ -f "$DEST/src/main.odin" ]
}

@test "odin: no foreign manifest created" {
    run_scaffold "odin"
    [ ! -f "$DEST/build.zig.zon" ]
    [ ! -f "$DEST/Cargo.toml" ]
    [ ! -f "$DEST/go.mod" ]
    [ ! -f "$DEST/pyproject.toml" ]
}

@test "odin: TEMPLATE is mise-odin-template" {
    run_scaffold "odin"
    grep -q 'TEMPLATE.*"mise-odin-template"' "$DEST/mise.toml"
}

@test "odin: CLAUDE.md contains Odin conventions" {
    run_scaffold "odin"
    grep -q "odin build" "$DEST/CLAUDE.md"
    grep -q "no bundled formatter\|no 'odin fmt'\|no .odin fmt." "$DEST/CLAUDE.md"
}

@test "odin: sample-code.txt removed" {
    run_scaffold "odin"
    [ ! -f "$DEST/src/sample-code.txt" ]
}

@test "odin: mise-tasks/ scripts are executable" {
    run_scaffold "odin"
    [ -x "$DEST/mise-tasks/publish/_default" ]
    [ -x "$DEST/mise-tasks/build/prod" ]
}

@test "odin: publish is source-only (no cross-compiled binaries)" {
    run_scaffold "odin"
    # Odin ships as source: no build:all-platforms task, and publish uploads no binaries.
    [ ! -f "$DEST/mise-tasks/build/all-platforms" ]
    grep -q 'gh release' "$DEST/mise-tasks/publish/_default"
    ! grep -q 'build:all-platforms' "$DEST/mise-tasks/publish/_default"
    ! grep -q 'release upload' "$DEST/mise-tasks/publish/_default"
}

@test "odin: project name replaced in src/lib.odin" {
    run_scaffold "odin" "my-lib"
    grep -q 'my_lib' "$DEST/src/lib.odin"
}

@test "odin: mise.toml uses github odin backend" {
    run_scaffold "odin"
    grep -q 'github:odin-lang/Odin' "$DEST/mise.toml"
}

@test "odin: ci.yml has publish-rc job calling publish-rc task" {
    run_scaffold "odin"
    grep -q 'mise run publish:rc' "$DEST/.github/workflows/ci.yml"
}

@test "odin: ci.yml has no template-only tasks" {
    run_scaffold "odin"
    ! grep -q 'templates:' "$DEST/.github/workflows/ci.yml"
}

@test "odin: mise-tasks/publish-rc is executable" {
    run_scaffold "odin"
    [ -x "$DEST/mise-tasks/publish/rc" ]
}

@test "odin: nested override replaced base (publish)" {
    run_scaffold "odin"
    # Odin's publish override is source-only (differs from the base TODO stub).
    grep -q 'source release' "$DEST/mise-tasks/publish/_default"
}

@test "odin: format tasks are no-op stubs" {
    run_scaffold "odin"
    grep -q 'no-op\|WARNING' "$DEST/mise-tasks/format/_default"
    grep -q 'WARNING' "$DEST/mise-tasks/format/check"
}

@test "odin: all task files executable (incl. nested)" {
    run_scaffold "odin"
    assert_tasks_executable "$DEST"
}
