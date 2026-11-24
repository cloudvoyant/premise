import { createServer, type IncomingMessage, type Server, type ServerResponse } from "node:http";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { dirname } from "node:path";
import {
  buildCassette,
  CassetteReplay,
  sanitizeHeaders,
  type Cassette,
  type Exchange,
  type ExchangeRequest,
  type ExchangeResponse,
} from "./cassette.js";
import type { E2EMode, Json } from "./mode.js";

export type CassetteFile = Record<string, Cassette>;

export interface ProxyOptions {
  mode: E2EMode;
  provider: ExchangeRequest["provider"];
  cassettePath: string;
  journeyKey: string;
  upstreamBaseUrl?: string;
  upstreamHeaders?: Record<string, string>;
  forbiddenSecrets?: string[];
  runId: string;
  repository?: string;
}

async function readRequestBody(req: IncomingMessage): Promise<Json | string | null> {
  const chunks: Buffer[] = [];
  for await (const chunk of req) {
    chunks.push(typeof chunk === "string" ? Buffer.from(chunk) : chunk);
  }
  if (chunks.length === 0) return null;
  const text = Buffer.concat(chunks).toString("utf8");
  if (!text) return null;
  try {
    return JSON.parse(text) as Json;
  } catch {
    return text;
  }
}

/**
 * Loopback HTTP proxy supporting strict offline replay, live recording with redaction,
 * and live pass-through without overwriting committed cassettes.
 */
export class HttpCassetteProxy {
  private server?: Server;
  private replay?: CassetteReplay;
  private readonly recorded: Exchange[] = [];
  private endpoint = "";
  private failure?: Error;

  constructor(private readonly options: ProxyOptions) {}

  get url(): string {
    return this.endpoint;
  }

  async start(): Promise<string> {
    if (this.options.mode === "replay") {
      const raw = await readFile(this.options.cassettePath, "utf8");
      const file = JSON.parse(raw) as CassetteFile;
      const cassette = file[this.options.journeyKey];
      if (!cassette) {
        throw new Error(
          `Missing cassette journey "${this.options.journeyKey}" in ${this.options.cassettePath}. Refresh with mise run e2e:record.`,
        );
      }
      this.replay = new CassetteReplay(cassette);
    }

    this.server = createServer((req, res) => {
      void this.handle(req, res);
    });
    await new Promise<void>((resolve, reject) => {
      this.server!.once("error", reject);
      this.server!.listen(0, "127.0.0.1", () => resolve());
    });
    const address = this.server.address();
    if (!address || typeof address === "string") {
      throw new Error("Failed to bind loopback cassette proxy");
    }
    this.endpoint = `http://127.0.0.1:${address.port}`;
    return this.endpoint;
  }

  private async handle(req: IncomingMessage, res: ServerResponse): Promise<void> {
    try {
      const body = await readRequestBody(req);
      const exchangeReq: ExchangeRequest = {
        provider: this.options.provider,
        method: (req.method ?? "GET").toUpperCase(),
        path: req.url ?? "/",
        body,
      };

      let exchangeRes: ExchangeResponse;
      if (this.options.mode === "replay") {
        exchangeRes = this.replay!.respond(exchangeReq);
      } else {
        if (!this.options.upstreamBaseUrl) {
          throw new Error(`Live/record mode requires upstreamBaseUrl for ${this.options.provider}`);
        }
        const target = `${this.options.upstreamBaseUrl.replace(/\/+$/, "")}${exchangeReq.path}`;
        const headers: Record<string, string> = {
          ...(this.options.upstreamHeaders ?? {}),
        };
        if (req.headers["content-type"]) {
          headers["content-type"] = String(req.headers["content-type"]);
        }
        const upstream = await fetch(target, {
          method: exchangeReq.method,
          headers,
          body:
            exchangeReq.body === null
              ? undefined
              : typeof exchangeReq.body === "string"
                ? exchangeReq.body
                : JSON.stringify(exchangeReq.body),
          signal: AbortSignal.timeout(30_000),
        });
        const rawText = await upstream.text();
        let parsedBody: ExchangeResponse["body"] = null;
        if (rawText) {
          try {
            parsedBody = JSON.parse(rawText) as Json;
          } catch {
            parsedBody = rawText;
          }
        }
        const responseHeaders: Record<string, string> = {};
        upstream.headers.forEach((v, k) => {
          responseHeaders[k] = v;
        });
        exchangeRes = {
          status: upstream.status,
          headers: sanitizeHeaders(responseHeaders),
          body: parsedBody,
        };
        if (this.options.mode === "record") {
          this.recorded.push({ request: exchangeReq, response: exchangeRes });
        }
      }

      const contentType =
        exchangeRes.headers["content-type"] ??
        (typeof exchangeRes.body === "string" ? "text/plain; charset=utf-8" : "application/json");
      res.writeHead(exchangeRes.status, { "content-type": contentType });
      if (exchangeRes.body !== null) {
        res.end(
          typeof exchangeRes.body === "string"
            ? exchangeRes.body
            : JSON.stringify(exchangeRes.body),
        );
      } else {
        res.end();
      }
    } catch (error) {
      this.failure = error instanceof Error ? error : new Error(String(error));
      res.writeHead(500, { "content-type": "application/json" });
      res.end(JSON.stringify({ error: this.failure.message }));
    }
  }

  async stop(options: { testFailed?: boolean; gitBundle?: string } = {}): Promise<void> {
    if (this.server) {
      await new Promise<void>((resolve) => this.server!.close(() => resolve()));
      this.server = undefined;
    }
    if (this.failure) {
      throw this.failure;
    }
    if (this.options.mode === "replay" && !options.testFailed) {
      this.replay?.verify();
    }
    if (this.options.mode === "record" && !options.testFailed) {
      let existing: CassetteFile = {};
      try {
        existing = JSON.parse(await readFile(this.options.cassettePath, "utf8")) as CassetteFile;
      } catch {
        existing = {};
      }
      existing[this.options.journeyKey] = buildCassette(
        {
          runId: this.options.runId,
          repository: this.options.repository,
          recordedAt: new Date().toISOString(),
        },
        this.recorded,
        this.options.forbiddenSecrets ?? [],
        options.gitBundle,
      );
      await mkdir(dirname(this.options.cassettePath), { recursive: true });
      await writeFile(this.options.cassettePath, `${JSON.stringify(existing, null, 2)}\n`, "utf8");
    }
  }
}
