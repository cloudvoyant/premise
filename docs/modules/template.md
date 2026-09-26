# Template Module

## Purpose

The template module owns template directory safety, template initialization, file rendering, and execution of template task contracts. It operates on templates already identified by the registry or generate modules.

## Design

### API

| API                                                          | Use                                                              |
| ------------------------------------------------------------ | ---------------------------------------------------------------- |
| `TemplateDirectory(sourceRoot, templatePath)`                | Resolve and validate a template path beneath its source root.    |
| `RenderTemplateMise(kind)`                                   | Render the default Mise contract for an app or library template. |
| `InitializeTemplate(root, kind)`                             | Create a live template skeleton in a registry workspace.         |
| `TestTemplateContracts(ctx, root, manifest, stdout, stderr)` | Run every declared template’s task contracts.                    |
| `ResolveTemplateSource(ctx, workspaceRoot, selector)`        | Resolve a selector to a local or checked-out source root.        |

### Usage

```go
path, err := core.InitializeTemplate(registryRoot, "app")
if err != nil {
    return err
}
fmt.Fprintf(output, "initialized %s\n", path)
```

To validate all templates declared by a registry:

```go
err := core.TestTemplateContracts(ctx, root, manifest, stdout, stderr)
```

## Implementation Details

Template paths are cleaned, resolved, and checked against the source root before use. Initialization creates a temporary candidate, writes the template declaration and default Mise contract, and renames the completed candidate into place so callers do not observe partial output.

Contract testing maps template kinds to their required task ceilings and executes those tasks through the Mise subprocess boundary. Generate uses the same internal contract runner against a disposable complete-workspace candidate before publication.

The template module does not choose a registry entry or persist a generated project. Selection belongs to [Registry](registry.md), while generation planning and publication belong to [Generate](generate.md).
