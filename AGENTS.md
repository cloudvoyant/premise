# Go file organization

Follow the section labels used in `core/template.go`:

- Put exported functions and methods under `// Public API -----------------------------------------------------------------`.
- Put unexported functions, helper types, and methods on unexported types at the bottom of the file under `// Private helpers ------------------------------------------------------------`.
- Keep each declaration's documentation with that declaration.

For Go test files, use these sections instead of `Public API`, in this order:

- `// Fixtures -------------------------------------------------------------------` for shared fixture types, their methods, and fixture setup functions.
- `// Tests ----------------------------------------------------------------------` for `Test*`, `Benchmark*`, `Fuzz*`, and `Example*` functions.
- `// Private helpers ------------------------------------------------------------` for other test helpers at the bottom of the file.

Omit sections that have no declarations. Keep related test groups within the `Tests` section.

# GitHub Actions

Use released version tags for actions instead of long commit hashes where a
compatible release exists. Keep generated workflows in `assets/github-workflows`
consistent with the repository workflows. A release candidate tag is acceptable
for a tandem PR that needs an interface absent from the stable release.
