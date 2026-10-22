# Managing Premise monorepos

A workspace records applications under `apps/` and libraries under `libs/` in `premise.yaml`. Each project owns its `mise.toml`. Root Mise configuration supplies shared tools and workspace tasks.

## Add and inspect projects

```sh
pm g :premise-tanstack-start-app
pm install
pm projects ls --flow on-commit --json
```

Templates define the app or library contract and runnable tasks. Generation creates new projects; it does not update existing projects. See [project creation](projects.md) for registry selectors and template authoring.

## Run workspace tasks

```sh
pm run web:test
mise run --jobs 1 '//...:build'
mise run --jobs 1 '//...:test'
```

Use a project selector for one project and Mise selectors for workspace fan-out. Root files shared by template registries follow the [generation merge rules](../generation.md).

## Check and release changes

[Automated CI](ci.md) describes checks, runner selection, releases, previews, and environment promotion. Only workspaces with infrastructure create deployment environments. Libraries use release publication without environment deployment.
