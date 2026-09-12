# Contributing

## Getting Started

Fork and clone the repository:

```bash
git clone https://github.com/cloudvoyant/premise.git
cd premise
mise install                     # Install tools declared in mise.toml
mise run install                 # Install prettier and download Go modules
```

## Development Workflow

Make your changes:

```bash
git checkout -b feature/my-feature
# Make changes
mise run build
mise run test
```

Commit using conventional commit format:

```bash
git commit -m "feat: add new feature"
git commit -m "fix: resolve bug"
git commit -m "docs: update readme"
```

Push and create a pull request:

```bash
git push origin feature/my-feature
```

## Commit Message Format

Use conventional commits for automatic versioning:

- `feat:` - New feature (minor version bump)
- `fix:` - Bug fix (patch version bump)
- `docs:` - Documentation changes
- `style:` - Code style changes (formatting, etc.)
- `refactor:` - Code refactoring
- `test:` - Adding or updating tests
- `chore:` - Maintenance tasks

Breaking changes:

```bash
git commit -m "feat!: breaking change description"
```

or:

```bash
git commit -m "feat: description

BREAKING CHANGE: explanation of breaking change"
```

## Code Style

- Follow `.editorconfig` settings
- LF line endings
- Insert final newline
- Trim trailing whitespace

## Testing

Run tests before submitting:

```bash
mise run test
```

Ensure CI passes on your pull request.

## Documentation

Update documentation when:

- Adding new features
- Changing behavior
- Adding new commands

Documentation files:

- `README.md` - Quick start and overview
- `docs/user-guide.md` - Setup and usage guide
- `docs/architecture.md` - Design, architecture, and implementation
- `templates/README.md` - Language template catalog and task contract

Follow the documentation style guide:

- Be concise and scannable
- Use backticks for files, commands, and code
- Avoid excessive bold formatting

## Pull Request Process

1. Create a feature branch
2. Make your changes
3. Run `mise run build && mise run test`
4. Commit with conventional commit messages
5. Push and create PR
6. Wait for CI to pass
7. Address review feedback
8. Maintainer merges when approved

## Release Process

Release automation is being replaced with svu and GoReleaser in [issue #2](https://github.com/cloudvoyant/premise/issues/2). Do not create a release until that workflow is complete.
