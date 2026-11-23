import { describe, expect, it } from "vitest";
import { createServer } from "node:http";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { assertNoLeakedSecrets, buildCassette, CassetteReplay } from "./cassette.js";
import { HttpCassetteProxy } from "./cassette-proxy.js";

describe("given the HTTP and Git cassette engine", () => {
  it("when replaying a valid cassette, then reads and ordered writes match without network access", () => {
    const cassette = buildCassette({ runId: "run-1", recordedAt: "2026-11-22T00:00:00Z" }, [
      {
        request: {
          provider: "github",
          method: "GET",
          path: "/repos/cloudvoyant/premise-e2e",
          body: null,
        },
        response: {
          status: 200,
          headers: { "content-type": "application/json" },
          body: { private: true },
        },
      },
      {
        request: {
          provider: "github",
          method: "POST",
          path: "/repos/cloudvoyant/premise-e2e/releases",
          body: { tag_name: "v0.1.0" },
        },
        response: {
          status: 201,
          headers: { "content-type": "application/json" },
          body: { id: 42 },
        },
      },
    ]);
    const replay = new CassetteReplay(cassette);

    const getRes = replay.respond({
      provider: "github",
      method: "GET",
      path: "/repos/cloudvoyant/premise-e2e",
      body: null,
    });
    const postRes = replay.respond({
      provider: "github",
      method: "POST",
      path: "/repos/cloudvoyant/premise-e2e/releases",
      body: { tag_name: "v0.1.0" },
    });

    expect(getRes.status).toBe(200);
    expect(postRes.status).toBe(201);
    expect(() => replay.verify()).not.toThrow();
  });

  it("when an unrecorded request or missing write occurs, then replay fails closed", () => {
    const cassette = buildCassette({ runId: "run-2", recordedAt: "2026-11-22T00:00:00Z" }, [
      {
        request: {
          provider: "npm",
          method: "PUT",
          path: "/@cloudvoyant%2fpkg",
          body: { name: "@cloudvoyant/pkg" },
        },
        response: { status: 201, headers: {}, body: { ok: true } },
      },
    ]);
    const replay = new CassetteReplay(cassette);

    expect(() =>
      replay.respond({ provider: "npm", method: "GET", path: "/unexpected", body: null }),
    ).toThrow(/Unrecorded npm request/);
    expect(() => replay.verify()).toThrow(/Replay omitted 1 recorded write/);
  });

  it("when recording a response with tokens or private user fields, then secrets are redacted or rejected", async () => {
    const dir = await mkdtemp(join(tmpdir(), "premise-cassette-test-"));
    const secretToken = "ghp_superSecretTokenValue123456";
    const upstream = createServer((_req, res) => {
      res.writeHead(200, {
        "content-type": "application/json",
        authorization: `Bearer ${secretToken}`,
        "set-cookie": "session=secret",
      });
      res.end(
        JSON.stringify({
          login: "bot",
          email: "private@example.com",
          echo: `token=${secretToken}`,
        }),
      );
    });
    await new Promise<void>((resolve) => upstream.listen(0, "127.0.0.1", () => resolve()));
    const addr = upstream.address();
    const upstreamUrl = `http://127.0.0.1:${typeof addr === "object" && addr ? addr.port : 0}`;
    const cassettePath = join(dir, "test.json");

    try {
      const recorder = new HttpCassetteProxy({
        mode: "record",
        provider: "github",
        cassettePath,
        journeyKey: "redaction-check",
        upstreamBaseUrl: upstreamUrl,
        forbiddenSecrets: [secretToken],
        runId: "run-redact",
      });
      const proxyUrl = await recorder.start();
      const liveRes = await fetch(`${proxyUrl}/user`);
      expect(liveRes.status).toBe(200);
      await recorder.stop();

      const saved = await readFile(cassettePath, "utf8");
      expect(saved).not.toContain(secretToken);
      expect(saved).not.toContain("private@example.com");
      expect(saved).toContain("[REDACTED]");
      expect(() => assertNoLeakedSecrets(`leaked ${secretToken}`, [secretToken])).toThrow(
        /raw secret detected/,
      );

      const replayer = new HttpCassetteProxy({
        mode: "replay",
        provider: "github",
        cassettePath,
        journeyKey: "redaction-check",
        runId: "run-redact",
      });
      const replayUrl = await replayer.start();
      const replayed = await fetch(`${replayUrl}/user`);
      expect(replayed.status).toBe(200);
      const body = (await replayed.json()) as { login: string; echo: string; email?: string };
      expect(body.login).toBe("bot");
      expect(body.echo).toBe("token=[REDACTED]");
      expect(body.email).toBeUndefined();
      await replayer.stop();
    } finally {
      await new Promise<void>((resolve) => upstream.close(() => resolve()));
      await rm(dir, { recursive: true, force: true });
    }
  });
});
