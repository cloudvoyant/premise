<!-- @context: python, uv, ruff, pytest -->

## Python Development (uv template)

**Package manager**: Always use `uv run <cmd>`, never invoke `python` directly.

**Common commands:**
- `mise run install` — sync deps (`uv sync --all-extras`)
- `mise run test` — run pytest
- `mise run lint` / `mise run lint-fix` — ruff check / auto-fix
- `mise run format` / `mise run format-check` — ruff format
- `mise run build` — build wheel + sdist to `dist/`
- `mise run publish` — publish to PyPI (requires `UV_PUBLISH_TOKEN`)

**Adding dependencies:**
- Runtime: `uv add <package>`
- Dev/test only: `uv add --dev <package>`

**Code style (ruff):**
- Line length: 120 chars
- Rules: E, F, I (isort), UP (pyupgrade), B (bugbear), SIM
- Run `mise run lint-fix` to auto-fix, `mise run format` for formatting

**Testing (pytest):**
- Tests in `tests/` directory; run with `mise run test`
- Stop on first failure: `uv run pytest -x`
- Filter by name: `uv run pytest -k "test_add"`
- Coverage: `uv run pytest --cov=src`

**Publishing:**
- `mise run publish` — calls `uv publish`
- Set `UV_PUBLISH_TOKEN` env var or use PyPI trusted publishing (OIDC) in CI
