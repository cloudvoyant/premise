import { chmod, mkdir, writeFile } from "node:fs/promises";
import { join } from "node:path";
import type { PremiseHarness } from "./premise-harness.js";

// Public API -----------------------------------------------------------------

/** Write executable shell shims into `<outputDir>/<dirName>` and return that bin directory. */
export async function installCommandShims(
  outputDir: string,
  dirName: string,
  shims: Record<string, string>,
): Promise<string> {
  const binDir = join(outputDir, dirName);
  await mkdir(binDir, { recursive: true });
  for (const [name, script] of Object.entries(shims)) {
    const file = join(binDir, name);
    await writeFile(file, script, { mode: 0o755 });
    await chmod(file, 0o755);
  }
  return binDir;
}

/** Install a `pm` wrapper shim pointing at the harness's built Premise binary. */
export function installPmShim(h: PremiseHarness, dirName = "pm-bin"): Promise<string> {
  return installCommandShims(h.outputDir, dirName, {
    pm: `#!/bin/sh\nexec "${h.binary}" "$@"\n`,
  });
}

/** Install a replay-mode `gcloud` shim that writes an external_account ADC file from CI_OIDC_TOKEN_FILE. */
export function installGcloudOidcReplayShim(h: PremiseHarness): Promise<string> {
  return installCommandShims(h.outputDir, "gcloud-shim", {
    gcloud: `#!/bin/sh
set -eu
out=""
for arg in "$@"; do
  case "$arg" in
    --output-file=*) out="\${arg#--output-file=}" ;;
  esac
done
[ -n "$out" ]
printf '{"type":"external_account","credential_source":{"file":"%s"}}\n' "$CI_OIDC_TOKEN_FILE" > "$out"
`,
  });
}

/** Install replay-mode `pm`, `mise`, and `goreleaser` shims for source-only release action tests. */
export function installSourceReleaseShims(h: PremiseHarness): Promise<string> {
  return installCommandShims(h.outputDir, "shim-bin", {
    pm: `#!/bin/sh\nexec "${h.binary}" "$@"\n`,
    mise: `#!/bin/sh
set -eu
[ -z "\${GITHUB_TOKEN:-}" ]
[ -z "\${GH_TOKEN:-}" ]
[ -z "\${NODE_AUTH_TOKEN:-}" ]
[ -z "\${CRATES_TOKEN:-}" ]
case "$*" in
 exec\\ goreleaser@2.18.1\\ --\\ env\\ -0) /usr/bin/env -0 ;;
 *) echo "unexpected mise invocation: $*" >&2; exit 1 ;;
esac
`,
    goreleaser: `#!/bin/sh
set -eu
[ -n "\${GITHUB_TOKEN:-}" ]
[ -z "\${GH_TOKEN:-}" ]
[ -z "\${NODE_AUTH_TOKEN:-}" ]
[ -z "\${CRATES_TOKEN:-}" ]
[ "$1" = release ]
while [ "$#" -gt 0 ]; do
 case "$1" in
  -f) shift; configuration=$1 ;;
  --snapshot) echo "source-only build should skip artifact tools" >&2; exit 1 ;;
 esac
 shift
done
[ "$(pwd -P)" = "$EXPECTED_RELEASE_ROOT" ]
grep -q 'skip: true' "$configuration"
grep -q 'disable: true' "$configuration"
! grep -q 'extra_files:' "$configuration"
printf 'publish\\n' >> "$RELEASE_CAPTURE"
[ "\${FAIL_PUBLICATION:-false}" != true ]
`,
  });
}
