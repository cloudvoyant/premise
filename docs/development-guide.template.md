# Development Guide

> Developer onboarding and workflow guide for {{PROJECT_NAME}}

## Getting Started

### Prerequisites

Before you begin, ensure you have the following installed:

- `git` - Version control
- `mise` - Tool and task runner ([installation](https://mise.jdx.dev/getting-started.html))

### Initial Setup

1. Clone the repository:

   ```bash
   git clone https://github.com/YOUR-ORG/{{PROJECT_NAME}}.git
   cd {{PROJECT_NAME}}
   ```

2. Install tools:

   ```bash
   mise install
   ```

   `mise install` installs all tools declared in `mise.toml` (node, shellcheck, shfmt, gcloud, claude, etc.).

### Build & Run

```bash
mise run run    # To run executable
mise run test   # To run tests
```

## Project Structure

```
{{PROJECT_NAME}}/
├── .github/           # GitHub Actions workflows
├── .claude/           # AI assistant configuration
├── mise-tasks/       # File-based mise tasks (typically bash scripts)
├── docs/              # Documentation
├── src/               # Source code
├── test/              # Tests
├── mise.toml          # Tools, env vars, and task definitions
└── README.md          # Project overview
```

## Development Workflow

### Commands

Common commands using `mise run`:

```bash
mise tasks         # List all available tasks
mise run build     # Build the project
mise run run       # Run locally
mise run test      # Run tests
mise run format    # Format code
mise run lint      # Lint code
mise run clean     # Clean build artifacts
```

### Making Changes

1. Create a feature branch:

   ```bash
   git checkout -b feature/your-feature-name
   ```

2. Make your changes:
   - Write code
   - Add tests
   - Update documentation

3. Test your changes, lint and format:

   ```bash
   mise run test
   mise run lint
   mise run format:check
   ```

4. Commit using conventional commits:

   ```bash
   git commit -m "feat: add new capability"
   git commit -m "fix: resolve issue with X"
   git commit -m "docs: update README"
   git commit -m "refactor: restructure component Y"
   ```

5. Push and create pull request:

   ```bash
   git push -u origin feature/your-feature-name
   ```

6. Monitor CI for pre-release build artifacts

### Pull Request Process

1. Open PR on GitHub
2. Ensure CI passes:
   - All tests pass
   - Code is formatted
   - No linting errors
3. Request review from team members
4. Address feedback and push updates
5. Merge when approved

## Development Environment

You can configure your development environment in the following ways:

### Using Mise

This is the recommended way of working with this project. Simply run `mise install` to install dependencies in a project-scoped environment.

You can alternately use mise to install tools in a global scope, or use your preferred way to manually manage local dependencies.

## AI-Assisted Development

This project uses Claude Code for AI-assisted development:

```bash
# Install Claude CLI (if not already installed)
npm install -g @anthropic-ai/claude-cli

# Verify installation
claude --version
```

### Claude Code Commands

Custom commands available in `.claude/commands/`:

```bash
# List available commands
ls .claude/commands/

# Use a command (in Claude Code CLI)
/command-name
```

## Resources

### Documentation

- [Architecture Guide](./architecture.md)
- [User Guide](./user-guide.md)
- [Infrastructure Guide](./infrastructure.md)

### External Resources

TODO: Add links to relevant external resources:

- Language-specific docs
- Framework documentation
- API references

---

Template: {{TEMPLATE_NAME}} v{{TEMPLATE_VERSION}}
