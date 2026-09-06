# {{PROJECT_NAME}} Development Guide

Generated from {{TEMPLATE_NAME}} v{{TEMPLATE_VERSION}}.

## Prerequisites

- [mise](https://mise.jdx.dev/) — manages all tool versions (Python, uv, node, etc.)
- Python 3.12+ is installed automatically by mise via `mise install`

## Getting Started

```bash
# Install all tools and dependencies
mise install
mise run install

# Run tests
mise run test

# Check code quality
mise run lint
mise run format:check
```

## Project Structure

```
src/{{PROJECT_NAME}}/      # Library source
tests/                      # pytest tests
pyproject.toml              # Package metadata, ruff/pytest config
mise.toml                   # Task runner and tool versions
```

## Development Workflow

1. **Add a feature**: write code in `src/{{PROJECT_NAME}}/`, add tests in `tests/`
2. **Check quality**: `mise run lint && mise run format:check`
3. **Run tests**: `mise run test`
4. **Fix issues**: `mise run lint:fix && mise run format`

## Adding Dependencies

```bash
uv add requests          # runtime dependency
uv add --dev pytest-cov  # dev-only dependency
```

## Publishing to PyPI

1. Set `UV_PUBLISH_TOKEN` to your PyPI API token (or configure OIDC trusted publishing)
2. Push to `main` branch — CI runs `mise run upversion` then `mise run publish`
3. Or manually: `mise run build && mise run publish`
