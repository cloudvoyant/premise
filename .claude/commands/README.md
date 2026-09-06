# Claude Commands

This template uses the **Claudevoyant** plugin for powerful slash commands that help with template and project management.

## Installation

The Claudevoyant plugin is **automatically installed** when you run:

```bash
mise run install-claude-plugins
```

The plugin provides all slash commands like `/plan`, `/commit`, `/upgrade`, etc.

### Manual Installation

If you need to install or reinstall the plugin manually, first add the marketplace:

```bash
claude plugin marketplace add cloudvoyant/claudevoyant
```

Then install the plugin:

```bash
claude plugin install claudevoyant
```

Or for local development:

```bash
claude plugin marketplace add ../claudevoyant
claude plugin install claudevoyant
```

## Available Commands

### Template Commands (in this directory)

- `/upgrade` - Migrate project to latest template version
- `/adapt` - Adapt template to your project's needs (template-only, auto-deletes after use)

### Plugin Commands (from Claudevoyant)

Once the plugin is installed, you'll have access to these commands:

#### Project Management

- `/spec:new` - Create a new plan by exploring requirements
- `/spec:init` - Initialize an empty plan template
- `/spec:refresh` - Review and update plan status
- `/spec:pause` - Capture insights from planning session
- `/spec:go` - Execute the plan with spec-driven development
- `/spec:done` - Mark plan as complete and optionally commit

#### Development Workflow

- `/dev:commit` - Create conventional commit with proper formatting
- `/dev:review` - Perform comprehensive code review
- `/dev:docs` - Validate documentation completeness

#### Architecture & Decisions

- `/adr:new` - Create new Architectural Decision Record
- `/adr:capture` - Capture decisions from current session as ADRs

## Documentation

For detailed command documentation, see the [Claudevoyant plugin repository](https://github.com/claudevoyant/claudevoyant).

## Updating Commands

To update to the latest version of the commands:

```bash
claude plugin update claudevoyant
```

## Plugin Source

The plugin source code and documentation is maintained separately at:
https://github.com/claudevoyant/claudevoyant
