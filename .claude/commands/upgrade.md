Help me migrate this project to the latest mise-lib-template version using a spec-driven approach.

## Overview

This workflow helps you systematically upgrade your project to the latest template version by:

1. Detecting your current template version
2. Cloning the latest template for comparison
3. Creating a migration plan with all files to review
4. Working through changes methodically
5. Testing and validating the migration

## Breaking Changes: 1.x → 2.x (just/direnv → mise)

If your project is on **1.x** (has `justfile`, `.envrc`, `scripts/`), you are migrating to a fundamentally different toolchain. Here is a summary of what changed:

| 1.x                   | 2.x                                    |
| --------------------- | -------------------------------------- |
| `justfile`            | `mise.toml` `[tasks]` or `mise-tasks/` |
| `.envrc` (direnv)     | `mise.toml` `[env]`                    |
| `scripts/` bash files | `mise-tasks/` task scripts             |
| `just <task>`         | `mise run <task>`                      |
| `direnv allow`        | not needed                             |
| `scripts/utils.sh`    | `mise-tasks/utils`                     |
| `scripts/setup.sh`    | removed (use `mise install`)           |
| `[tools]` in justfile | `mise.toml` `[tools]`                  |

### 1.x → 2.x Migration Steps

1. **Create `mise.toml`** — move env vars from `.envrc` to `[env]`, tools from scripts to `[tools]`, and recipes from `justfile` to `[tasks]`
2. **Migrate scripts** — move `scripts/scaffold.sh`, `upversion.sh`, `utils.sh` to `mise-tasks/scaffold`, `upversion`, `utils` (remove the `.sh` extension; mise runs them directly); delete `toggle-files.sh` (replaced by static `.zed/settings.json`)
3. **Update CI workflows** — replace manual tool installs with `jdx/mise-action@v4`; add `mise run install` step before running tasks
4. **Replace `just` calls** — update any `just <task>` references in docs, CI, and scripts to `mise run <task>`
5. **Remove old files** — delete `justfile`, `.envrc`, `.envrc.template`, `scripts/setup.sh`
6. **Install dependencies** — run `mise install` to verify all tools install correctly

After migration, mark the version by adding a comment at the top of `mise.toml`:

```toml
# Template: mise-lib-template v2.x
```

---

## Steps

### 1. Detect Current Version

Check the template version this project is currently using. With mise-based projects (2.x), look for a comment at the top of `mise.toml`:

```toml
# Template: mise-lib-template v2.x
```

If no version comment exists, check `git log --oneline | head -20` to find when the project was scaffolded. Projects without `mise.toml` as their primary config are on 1.x.

### 2. Clone Latest Template

Clone the latest template to `.tmp/template-upstream-main` for comparison:

```bash
mkdir -p .tmp

if [ -d ".tmp/template-upstream-main" ]; then
    cd .tmp/template-upstream-main && git pull && cd ../..
else
    git clone https://github.com/cloudvoyant/mise-lib-template .tmp/template-upstream-main
fi
```

### 3. Create Migration Plan

Create `.claude/plan.md` with a structured migration plan:

```markdown
# Migration Plan: v<current> → v<target>

## Overview

Migrate from mise-lib-template v<current> to v<target>

## Files to Review

### Core Configuration

- [ ] mise.toml - Check for task, tool, and env changes
- [ ] mise-tasks/scaffold - Check for scaffolding improvements
- [ ] mise-tasks/upversion - Check for versioning updates
- [ ] mise-tasks/utils - Check for utility function updates

### CI/CD Workflows

- [ ] .github/workflows/ci.yml - Check for workflow updates
- [ ] .github/workflows/release.yml - Check for release changes

### Configuration Files

- [ ] .gitignore - Check for new patterns
- [ ] .gitattributes - Check for line ending rules
- [ ] .releaserc.json - Check for semantic-release config

### Claude Code Configuration

- [ ] .claude/CLAUDE.md - Check for instruction updates
- [ ] .claude/commands/\*.md - Check for new/updated commands

### IDE Configuration

- [ ] .zed/settings.json - Check for editor settings

### Documentation

- [ ] README.template.md - Check for documentation updates
- [ ] docs/architecture.md - Check for architecture changes
- [ ] docs/user-guide.md - Check for user guide updates

## Changes to Apply

For each file with differences, create tasks like:

### Task 1: Review mise.toml changes

- [ ] Compare: diff mise.toml .tmp/template-upstream-main/mise.toml
- [ ] Review changes and decide what to apply
- [ ] Apply relevant changes (preserve project customizations)
- [ ] Test: mise run build && mise run test

### Task 2: Review CI workflow changes

- [ ] Compare: diff .github/workflows/ci.yml .tmp/template-upstream-main/.github/workflows/ci.yml
- [ ] Compare: diff .github/workflows/release.yml .tmp/template-upstream-main/.github/workflows/release.yml
- [ ] Apply relevant changes

[Repeat for each file category]

### Task N: Update version comment

- [ ] Update template version comment in mise.toml to v<target>

## Testing

- [ ] Run: mise run test
- [ ] Run: mise run templates:test (if in template repo)
- [ ] Verify builds work
- [ ] Check CI passes

## Cleanup

- [ ] Remove: rm -rf .tmp/template-upstream-main
- [ ] Archive or delete migration plan
```

### 4. Work Through Plan Systematically

For each task in the migration plan:

#### a. Compare Files

```bash
diff mise.toml .tmp/template-upstream-main/mise.toml
diff -r mise-tasks/ .tmp/template-upstream-main/mise-tasks/
```

#### b. Review Changes

Determine if changes apply to this project:

- **Infrastructure changes** (workflows, `mise-tasks/`): Usually apply
- **Task changes** (`mise.toml [tasks]`): May need customization to preserve project-specific logic
- **Configuration** (`.gitignore`, `.releaserc.json`): Review carefully
- **Claude/IDE configs**: Apply improvements, preserve project-specific settings

#### c. Apply Changes

Apply relevant changes while preserving project-specific customizations:

- Merge task updates into `mise.toml`
- Copy improved `mise-tasks/` scripts
- Merge workflow updates
- Preserve project-specific logic

#### d. Mark Complete

Update `.claude/plan.md` to mark task as completed.

#### e. Test Incrementally

After applying each significant change:

```bash
mise run test
```

### 5. Update Version

After all changes applied, update the template version comment in `mise.toml`:

```toml
# Template: mise-lib-template v<new-version>
```

### 6. Final Validation

```bash
mise run test
mise run build

# Verify all expected files exist
ls -la mise-tasks/ .github/workflows/
```

### 7. Cleanup

```bash
rm -rf .tmp/template-upstream-main
mv .claude/plan.md .claude/migration-complete-$(date +%Y%m%d).md
# Or: rm .claude/plan.md
```

## Best Practices

- **Create plan first** - Don't apply changes ad-hoc
- **Review all diffs** - Understand what changed and why
- **Preserve customizations** - Don't blindly copy template files
- **Test incrementally** - Verify after each significant change
- **Commit before starting** - Clean working directory for safety
- **Document decisions** - Note in plan.md why you kept/skipped changes

## Common Issues

### No version tracking

If there's no template version comment in `mise.toml`, add one:

```bash
# Add at the top of mise.toml (after # mise.toml header comment)
# Template: mise-lib-template v<version>
```

### Conflicting Changes

If you've heavily customized files that also changed in the template:

1. Review the template change carefully
2. Manually apply the improvement to your customized version
3. Document the merge in plan.md

### Failed Tests After Migration

If tests fail after applying changes:

1. Review what changed
2. Verify `mise install` was run and all tools are available
3. Check if `mise run install` (npm deps) is needed
4. Consult the template's CHANGELOG.md for breaking changes
