# CI Module

## Purpose

The CI module maps stable lifecycle names to Mise tasks, selects monorepo or template-registry fallback behavior, and gates release-candidate and stable publication.

## Design

### API

| API                                                                    | Use                                                       |
| ---------------------------------------------------------------------- | --------------------------------------------------------- |
| `CIFlow`                                                               | Represent `on-commit`, `on-merge`, or `on-release`.       |
| `ParseCIFlow(value)`                                                   | Validate a CLI flow argument.                             |
| `CIReleaseMode`                                                        | Represent `auto`, `none`, `github`, or `packages`.        |
| `ParseCIReleaseMode(value)`                                            | Validate release-phase selection.                         |
| `RunCIFlow(ctx, root, flow, environment, releaseMode, stdout, stderr)` | Run one complete lifecycle and its guarded release phase. |

### Usage

```go
flow, err := core.ParseCIFlow("on-merge")
if err != nil {
    return err
}
mode, err := core.ParseCIReleaseMode("auto")
if err != nil {
    return err
}
return core.RunCIFlow(ctx, root, flow, "", mode, stdout, stderr)
```

The CLI equivalent is:

```text
pm ci flow on-release --environment stage --release auto
```

## Implementation Details

A root Mise task with the same name as the selected flow overrides fallback lifecycle tasks. Otherwise `workspace.kind` selects monorepo or template-registry behavior. Monorepos run root selectors; template registries run applicable contract tasks in each declared template directory.

Task matrices distinguish app and library capabilities. App-only deploy and end-to-end tasks do not run for libraries. Release flows validate the requested environment and pass it to tasks after `--`.

Release-candidate publication requires a feature-branch push whose HEAD commit message contains `[publish-rc]`. Pull requests, `main`, missing repositories, and unmarked commits do not publish an RC.

Stable publication runs only after a successful `on-merge` lifecycle. `CIReleaseMode` can run the complete release, suppress it, or isolate GitHub and language-package phases for credential separation.

The private `ciRunner` interface isolates command execution for tests. Its production adapter owns a `miseRunner` and calls the Mise subprocess boundary directly for task discovery and execution; it does not call the public Task module entry points. Publication delegates to the Release module. Context cancellation propagates through both.
