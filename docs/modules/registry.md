# Registry Module

## Purpose

The registry module discovers template registries, parses template selectors, resolves local or remote template sources, and exposes declared templates with source identity.

## Design

### API

| API                                                   | Use                                                                              |
| ----------------------------------------------------- | -------------------------------------------------------------------------------- |
| `ParseTemplateSelector(selector)`                     | Parse `<source>:<template>` into a source and template name.                     |
| `ClassifyGenerateSelector(argument)`                  | Distinguish interactive, official-name, and explicit-source generation requests. |
| `LoadRegistry(sourceRoot)`                            | Load template declarations from one repository root.                             |
| `DefaultRegistry(ctx)`                                | Merge official registries into a deterministic source-aware list.                |
| `ResolveOfficialTemplateName(ctx, name)`              | Resolve a bare name when exactly one official registry declares it.              |
| `Registry.Names()`                                    | List template names in stable order.                                             |
| `RegistryEntry.Selector()`                            | Return the fully qualified source/template selector.                             |
| `ResolveTemplateSource(ctx, workspaceRoot, selector)` | Resolve and load the selected registry source.                                   |
| `DetectProjectKind(root)`                             | Read the configured lifecycle kind for CI routing.                               |
| `InitializeTemplate(root, kind)`                      | Create and declare a new local template.                                         |
| `TestTemplateContracts(ctx, root, manifest, ...)`     | Validate every declared local template contract.                                 |

### Usage

```go
selector := "cloudvoyant/premise-bun:premise-hono-api"
selection, err := core.ParseTemplateSelector(selector)
if err != nil {
    return err
}
sourceRoot, selection, err := core.ResolveTemplateSource(ctx, workspaceRoot, selector)
```

A registry manifest declares templates independently of generated projects:

```yaml
template_registry:
  workspace_files: []
  templates:
    - name: premise-app
      kind: app
      path: templates/premise-app
```

## Implementation Details

Remote registries are cloned with go-git into a cache below the user cache directory. Cache keys derive from normalized repository identity, and later resolutions refresh the cached repository. GitHub shorthand, HTTPS, SSH, and local sources are normalized before resolution.

Registry loading reads only manifest declarations. Physical files under `templates/` are not discoverable unless declared. Duplicate names across official sources receive disambiguated labels; a bare name fails when it is ambiguous.

`DetectProjectKind` reads `workspace.kind`; it does not infer lifecycle behavior from directory or package files. A repository may contain both generated projects and template declarations, while its configured kind selects the fallback CI lifecycle.

The registry module identifies and retrieves templates. Generation owns materialization, and the task/template contract boundary owns execution-based validation.
