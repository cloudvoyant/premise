# Task Module

## Purpose

The task module provides the Mise-backed execution boundary for workspace tasks, project tasks, template contracts, and plugin publication tasks. Mise remains the source of task definitions and tool versions.

## Design

### API

| API                                                            | Use                                                               |
| -------------------------------------------------------------- | ----------------------------------------------------------------- |
| `RunRootTask(ctx, root, task, ...)`                            | Run one named task from the workspace root.                       |
| `RunProjectTask(ctx, root, project, task, ...)`                | Resolve a registered project and run one task from its directory. |
| `ContractTasks(kind)`                                          | Return required lifecycle task names for an app or library.       |
| `MiseTaskRunner.Run(ctx, directory, additions, arguments...)`  | Run a sanitized Mise command for a package-manager plugin.        |
| `MiseTaskRunner.TaskExists(ctx, directory, task)`              | Check whether a Mise task selector resolves.                      |
| `ExtractMiseConfig(label, data)`                               | Parse a typed Mise configuration.                                 |
| `MergeMiseConfigs(shared, selected, kind, identity, resolver)` | Merge registry-root and selected-template Mise configuration.     |

### Usage

```go
if err := core.RunProjectTask(ctx, root, "billing", "test", stdin, stdout, stderr); err != nil {
    return err
}
```

A plugin can inspect and execute its publication contract without constructing subprocesses directly:

```go
runner := core.MiseTaskRunner{Stdout: stdout, Stderr: stderr}
exists, err := runner.TaskExists(ctx, packageRoot, "publish")
if err == nil && exists {
    err = runner.Run(ctx, packageRoot, env, "run", "publish")
}
```

## Implementation Details

All execution reaches the private `miseRunner`. It sets the command directory, propagates cancellation through `exec.CommandContext`, sanitizes publication credentials, and permits only explicit environment additions at the call site.

`TaskExists` invokes `mise task info <task> --json`. A Mise “Task not found” exit is converted to `(false, nil)`; other failures remain errors.

Project task routing loads `premise.yaml`, resolves `workspace.projects`, constrains the project path to the workspace, and then invokes Mise. Root task routing starts directly from the workspace root.

Mise merging uses typed tool, task, and environment representations. Contract tasks retain stable names, while non-contract collisions are namespaced by registry identity. Environment conflicts pass through the configured merge-conflict resolver.

The task module does not decide which lifecycle to run. CI, generation, and release modules choose operations and delegate execution here.
