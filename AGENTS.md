# Go file organization

Follow the section labels used in `core/template.go`:

- Put exported functions and methods under `// Public API -----------------------------------------------------------------`.
- Put unexported functions, helper types, and methods on unexported types at the bottom of the file under `// Private helpers ------------------------------------------------------------`.
- Keep each declaration's documentation with that declaration.

# GitHub Actions

Use released version tags for actions instead of long commit hashes where a
compatible release exists. Keep generated workflows in `assets/github-workflows`
consistent with the repository workflows. A release candidate tag is acceptable
for a tandem PR that needs an interface absent from the stable release.

# Cobra commands

Declare every command as a named variable under `Public API`. Keep `init` last
in that section. Put execution helpers under `Private helpers`. Show command
registration explicitly instead of constructing commands in a loop.

# Docs

Put Usage before API sections. Use short sentences, working examples, and diagrams
for flows. Keep each module focused on its own commands and responsibilities.
