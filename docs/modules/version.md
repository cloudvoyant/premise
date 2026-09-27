# Version Module

## Purpose

The version module calculates repository versions, validates explicit bumps and release-candidate identifiers, and validates package-publication versions.

## Design

### API

| API                                                | Use                                                                               |
| -------------------------------------------------- | --------------------------------------------------------------------------------- |
| `CurrentVersion(root)`                             | Return the latest reachable stable SemVer tag.                                    |
| `NextVersion(root)`                                | Infer the next stable version from conventional commits.                          |
| `ParseVersionBump(value)`                          | Parse `patch`, `minor`, or `major`.                                               |
| `BumpedVersion(root, bump)`                        | Apply an explicit version component bump.                                         |
| `ValidateRCIdentifier(identifier)`                 | Validate one SemVer prerelease identifier.                                        |
| `ReleaseCandidateVersion(root, identifier)`        | Produce the next version with `rc.<identifier>`.                                  |
| `ValidatePackagePublicationVersion(version, task)` | Validate stable versus RC package publication and remove an optional leading `v`. |

### Usage

```go
next, err := core.NextVersion(root)
if err != nil {
    return err
}

rc, err := core.ReleaseCandidateVersion(root, os.Getenv("GITHUB_RUN_NUMBER"))
```

```go
version, err := core.ValidatePackagePublicationVersion("v1.2.3", "publish")
// version == "1.2.3"
```

## Implementation Details

Repository calculation uses the svu Go SDK rather than invoking an external executable. A mutex protects svu’s process-directory dependency so concurrent callers do not race while evaluating different repositories.

Only stable tags matching `vMAJOR.MINOR.PATCH` form the baseline. The repository must contain the externally created `v0.0.0` bootstrap tag. Unrelated and prerelease tags do not become stable baselines.

`NextVersion` delegates conventional-commit inference to svu. `BumpedVersion` supplies an explicit patch, minor, or major bump. `ReleaseCandidateVersion` starts from the next stable version and adds `rc.<identifier>`.

Package publication accepts `publish` only for stable versions and `publish:rc` only for prerelease versions. Build metadata is rejected so Cargo and Bun apply the same release policy.

The version module calculates strings only. Release owns tags and publication sequencing.
