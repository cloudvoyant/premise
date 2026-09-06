Help me adapt this template to my project's specific needs using a spec-driven approach.

## Overview

This workflow helps you customize this template for your specific use case by:

1. Understanding your requirements
2. Creating a comprehensive adaptation plan
3. Working through changes systematically
4. Testing and validating adaptations

## Steps

### 1. Understand Requirements

I'll ask you about:

- Project language and framework
- Build and test requirements
- Publishing targets (GCP, npm, etc.)
- CI/CD needs beyond SDK publishing? If so this template may not be fit for your needs.
- Additional tooling requirements

### 2. Create Adaptation Plan

I'll create `.claude/plan.md` with phases for:

```markdown
# Adaptation Plan: mise-lib-template → <your-project>

## Phase 1: Language Setup

- [ ] Update mise.toml [tasks.build] for <language>
- [ ] Update mise.toml [tasks.test] for <language>
- [ ] Add language-specific tools to mise.toml [tools]
- [ ] Update .gitignore for <language>

## Phase 2: Version Management

- [ ] Update .releaserc.json to use language-specific version file (package.json, pyproject.toml, Cargo.toml, etc.)
- [ ] Update semantic-release prepareCmd to write to your version file
- [ ] Update VERSION reading in mise.toml [env] to read from your version file
- [ ] Remove version.txt if no longer needed

## Phase 3: Publishing

- [ ] Update mise.toml [tasks.publish] for <target>
- [ ] Configure registry authentication
- [ ] Notify users of any changes needed for GitHub action secrets

## Phase 4: Tooling

- [ ] Add <tool> configuration
- [ ] Update mise.toml tasks for <tool>
- [ ] Add <tool> to CI workflows if needed

## Phase 5: Documentation

- [ ] Update README.md with project specifics
- [ ] Update user-guide.md with custom workflows
- [ ] Document custom tasks in mise.toml
```

### 3. Work Through Plan

For each adaptation:

1. Review current implementation
2. Make necessary changes
3. Test changes work
4. Mark task complete
5. Move to next task

### 4. Validate Adaptations

```bash
mise run test
mise run build
mise run lint && mise run format:check && mise run test
```

### 5. Update Documentation

Update project docs to reflect customizations:

- `docs/architecture.md` - document custom design decisions
- `docs/user-guide.md` - explain custom workflows
- `README.md` - update with project specifics

### 6. Cleanup

```bash
mv .claude/plan.md .claude/adaptation-complete-$(date +%Y%m%d).md
```

## Best Practices

- Create plan before making changes
- Test after each significant adaptation
- Keep language-agnostic logic in `mise-tasks/`
- Put language-specific logic in `mise.toml [tasks]`
- Document why you made specific choices
- Update README.md to reflect customizations

## What to Keep vs Change

### Always Keep (core framework)

- `mise-tasks/` - bash automation scripts
- `mise.toml` - environment, tools, and task configuration
- `.github/workflows/` - CI/CD structure
- mise pattern (`mise run <task>`)

### Customize (language-specific)

- `mise.toml [tasks]` (build, test, run, publish)
- `.gitignore` patterns
- `docs/` content for your project
- `mise.toml [env]` for needed configuration
- Publishing targets and authentication

### Optional Additions

- Language-specific linters/formatters
- Additional CI checks
- Custom deployment scripts in `mise-tasks/`
- Development tooling in `mise.toml [tools]`

## Version Management for Different Languages

The template uses `version.txt` as a placeholder. **You should replace this with your language's standard version file.**

### Node.js (package.json)

1. Update `.releaserc.json`:

```json
{
  "plugins": [
    "@semantic-release/commit-analyzer",
    "@semantic-release/release-notes-generator",
    "@semantic-release/changelog",
    "@semantic-release/npm",
    "@semantic-release/git",
    "@semantic-release/github"
  ]
}
```

2. Update `mise.toml` VERSION env var:

```toml
[env]
VERSION = "{{exec(command='node -p \"require(./package.json).version\" 2>/dev/null || echo 0.1.0')}}"
```

3. Remove `version.txt`

### Python (pyproject.toml)

1. Update `.releaserc.json` prepareCmd:

```json
{
  "prepareCmd": "sed -i 's/^version = .*/version = \"${nextRelease.version}\"/' pyproject.toml"
}
```

2. Update `mise.toml` VERSION env var:

```toml
[env]
VERSION = "{{exec(command='grep \"^version =\" pyproject.toml | cut -d\\'\"\\' -f2 || echo 0.1.0')}}"
```

3. Update git assets in `.releaserc.json`:

```json
{ "assets": ["CHANGELOG.md", "pyproject.toml"] }
```

### Go (VERSION file or go.mod)

1. Update `.releaserc.json` prepareCmd:

```json
{
  "prepareCmd": "echo ${nextRelease.version} > VERSION"
}
```

2. Update `mise.toml` VERSION env var:

```toml
[env]
VERSION = "{{exec(command='cat VERSION 2>/dev/null | tr -d [:space:] || echo 0.1.0')}}"
```

### Rust (Cargo.toml)

1. Update `.releaserc.json` prepareCmd:

```json
{
  "prepareCmd": "sed -i 's/^version = .*/version = \"${nextRelease.version}\"/' Cargo.toml"
}
```

2. Update `mise.toml` VERSION env var:

```toml
[env]
VERSION = "{{exec(command='grep \"^version =\" Cargo.toml | cut -d\\'\"\\' -f2 || echo 0.1.0')}}"
```

3. Update git assets:

```json
{ "assets": ["CHANGELOG.md", "Cargo.toml", "Cargo.lock"] }
```

### Keep version.txt only if:

- You're building a truly language-agnostic tool
- You don't have a standard version file for your ecosystem
- You want a simple, universal approach

## Cleanup

Now that adaptation is complete, this command will delete itself since it's only needed during template adaptation.

Action: Delete `.claude/commands/adapt.md`

This is a template-only command and should not be kept in adapted repositories.
