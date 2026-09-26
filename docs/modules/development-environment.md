# Development Environment Module

## Purpose

The development-environment module installs the tools declared by the workspace and its generated projects. It provides Premise’s environment setup entry point while leaving tool resolution and installation to Mise.

## Design

### API

| API                                          | Use                                                                |
| -------------------------------------------- | ------------------------------------------------------------------ |
| `InstallDevTools(ctx, root, stdout, stderr)` | Install workspace and project tools from their Mise configuration. |

### Usage

```go
err := core.InstallDevTools(ctx, workspaceRoot, stdout, stderr)
```

`pm install` calls this after validating that it is running from a Premise workspace root with a root `mise.toml`.

## Implementation Details

`InstallDevTools` delegates tool installation to the shared Mise subprocess boundary. It first installs tools declared by the workspace root and then discovers conventional project-level Mise files beneath `apps/` and `libs/`.

The module does not resolve application package dependencies or replace package managers. It installs development tools only. If project Mise configurations exist, `pm install` separately invokes the root `install` task through the [Task Module](task.md), allowing the workspace to coordinate package-manager installation.
