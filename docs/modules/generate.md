# Generate Module

## Purpose

The generate module turns a selected registry template into a validated workspace project. It owns questionnaire answers, substitutions, root-file merge planning, disposable candidate validation, rollback, and atomic publication.

## Design

### API

| API                                             | Use                                                                                      |
| ----------------------------------------------- | ---------------------------------------------------------------------------------------- |
| `GenerateOptions`                               | Supply questionnaire and merge-conflict behavior to the high-level workflow.             |
| `Generate(ctx, cwd, selector, options, output)` | Resolve inputs and execute one complete generation.                                      |
| `GenerateParameters`                            | Provide already resolved roots, paths, identities, substitutions, and conflict handling. |
| `BuildGeneratePlan(parameters)`                 | Prepare and validate generation without publishing final files.                          |
| `GeneratePlan`                                  | Hold planned root entries, merged Mise configuration, and private staging state.         |
| `ApplyGeneratePlan(ctx, plan, output)`          | Validate a prepared candidate and publish it atomically.                                 |
| `ResolveSubstitutions(template, answers)`       | Convert questionnaire answers into literal replacement values.                           |

### Usage

The CLI normally uses the high-level workflow:

```go
err := core.Generate(ctx, cwd, selector, core.GenerateOptions{
    Questionnaire: core.InteractiveQuestionnaire{},
}, output)
```

Tests and non-interactive integrations can separate planning from publication:

```go
plan, err := core.BuildGeneratePlan(core.GenerateParameters{
    RegistryRoot:     registry,
    TemplateRoot:     filepath.Join(registry, "templates", "premise-app"),
    ClientRepoRoot:   workspace,
    ProjectPath:      "apps/billing",
    TemplateKind:     "app",
    RegistryIdentity: "cloudvoyant/premise",
    TemplateIdentity: "premise-app",
})
if err != nil {
    return err
}
return core.ApplyGeneratePlan(ctx, plan, output)
```

## Implementation Details

`Generate` locates the client manifest, resolves the registry selector through the registry module, loads the template declaration, asks its questionnaire, resolves substitutions, and builds `GenerateParameters`.

`BuildGeneratePlan` validates source and destination boundaries before creating staging directories on the destination filesystem. It plans explicit registry-root files separately from the selected template tree and computes typed Mise merge results through the merge and task modules.

`ApplyGeneratePlan` first preflights existing client-root tool selectors that would change. It then materializes a disposable complete workspace candidate, installs its Mise environment, and runs root and selected-project contracts with `PREMISE_TEMPLATE_TEST=1`. Successful command output remains hidden; failure output is returned to the caller.

Only a fully validated candidate is published. The generated project uses a same-filesystem rename. Root-file publication records backups, and rollback restores prior values if any later operation fails. The project record is written only as part of the validated publication flow.

The generate module depends on [Configuration](configuration.md), [Registry](registry.md), [Project](project.md), and [Task](task.md). Registry resolves sources but never generates files; Project defines the persisted result but never performs generation.
