import { defineConfig } from "vitest/config";

export function suiteConfig(
  root: string,
  options: { include?: string[]; exclude?: string[] } = {},
) {
  return defineConfig({
    test: {
      root,
      tags: [
        { name: "extended", skip: process.env.PREMISE_E2E_EXTENDED !== "true" },
        ...[
          "e2e",
          "cli",
          "smoke",
          "workspace",
          "secrets",
          "ci",
          "release",
          "deploy",
          "github",
          "gcp",
          "azure",
          "pulumi",
          "fnox",
          "cargo",
          "bun",
          "go",
          "replay",
          "live",
          "known-bug",
        ].map((name) => ({ name })),
      ],
      include: options.include ?? [process.env.PREMISE_E2E_SPEC ?? "**/*.spec.ts"],
      exclude: ["**/.cache/**", "**/outputs/**", "**/testdata/**", ...(options.exclude ?? [])],
      testTimeout: 300_000,
      hookTimeout: 120_000,
      sequence: { hooks: "stack" },
      fileParallelism: false,
      maxWorkers: 1,
      reporters: ["verbose"],
      retry: 0,
    },
  });
}
