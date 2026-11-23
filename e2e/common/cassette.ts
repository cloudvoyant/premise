import type { Json } from "./test-utils.js";

export interface ExchangeRequest {
  provider: "github" | "npm" | "cargo" | "oidc" | "gcp";
  method: string;
  path: string;
  body: Json | string | null;
}

export interface ExchangeResponse {
  status: number;
  headers: Record<string, string>;
  body: Json | Json[] | string | null;
}

export interface Exchange {
  request: ExchangeRequest;
  response: ExchangeResponse;
}

export interface CassetteFixtureMeta {
  runId: string;
  repository?: string;
  recordedAt: string;
}

export interface Cassette {
  version: 1;
  fixture: CassetteFixtureMeta;
  gitBundle?: string;
  responses: ExchangeResponse[];
  exchanges: Array<{ request: ExchangeRequest; response: number }>;
}

const UUID_RE = /[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/gi;

const SECRET_HEADER_KEYS = new Set([
  "authorization",
  "proxy-authorization",
  "x-github-token",
  "set-cookie",
  "cookie",
  "content-encoding",
  "transfer-encoding",
  "content-length",
  "x-oauth-client-id",
  "x-oauth-scopes",
  "x-accepted-oauth-scopes",
  "x-github-request-id",
  "x-ratelimit-remaining",
  "x-ratelimit-reset",
  "x-ratelimit-used",
  "date",
]);

const PRIVATE_FIELDS = new Set([
  "email",
  "plan",
  "private_gists",
  "total_private_repos",
  "owned_private_repos",
  "disk_usage",
  "two_factor_authentication",
  "collaborators",
]);

export function isReadRequest(request: ExchangeRequest): boolean {
  if (request.method === "GET" || request.method === "HEAD") return true;
  if (
    request.path === "/graphql" &&
    typeof request.body === "object" &&
    request.body !== null &&
    /^\s*query\b/.test(String(request.body.query ?? ""))
  ) {
    return true;
  }
  return false;
}

export function sanitizeHeaders(headers: Record<string, string>): Record<string, string> {
  const clean: Record<string, string> = {};
  for (const [key, value] of Object.entries(headers)) {
    const lower = key.toLowerCase();
    if (SECRET_HEADER_KEYS.has(lower)) continue;
    clean[lower] = value;
  }
  return clean;
}

export function redactValue(value: unknown, forbiddenSecrets: string[] = []): unknown {
  if (typeof value === "string") {
    let out = value;
    for (const secret of forbiddenSecrets) {
      if (secret && secret.length >= 6 && out.includes(secret)) {
        out = out.replaceAll(secret, "[REDACTED]");
      }
    }
    return out;
  }
  if (Array.isArray(value)) {
    return value.map((item) => redactValue(item, forbiddenSecrets));
  }
  if (value !== null && typeof value === "object") {
    const result: Json = {};
    for (const [key, val] of Object.entries(value as Json)) {
      if (PRIVATE_FIELDS.has(key)) continue;
      result[key] = redactValue(val, forbiddenSecrets);
    }
    return result;
  }
  return value;
}

export function assertNoLeakedSecrets(serialized: string, forbiddenSecrets: string[]): void {
  for (const secret of forbiddenSecrets) {
    if (secret && secret.length >= 6 && serialized.includes(secret)) {
      throw new Error("Refusing to save cassette: raw secret detected in serialized payload");
    }
  }
}

function matchValue(expected: unknown, actual: unknown, bindings: Map<string, string>): boolean {
  if (typeof expected === "string" && typeof actual === "string") {
    const originals = expected.match(UUID_RE) ?? [];
    const values = actual.match(UUID_RE) ?? [];
    if (
      expected.replace(UUID_RE, "<uuid>") !== actual.replace(UUID_RE, "<uuid>") ||
      originals.length !== values.length
    ) {
      return false;
    }
    return originals.every((original, index) => {
      if (bindings.has(original)) return bindings.get(original) === values[index];
      if ([...bindings.values()].includes(values[index])) return false;
      bindings.set(original, values[index]);
      return true;
    });
  }
  if (
    expected === null ||
    actual === null ||
    typeof expected !== "object" ||
    typeof actual !== "object"
  ) {
    return expected === actual;
  }
  if (Array.isArray(expected) !== Array.isArray(actual)) return false;
  if (Array.isArray(expected) && Array.isArray(actual)) {
    if (expected.length !== actual.length) return false;
    return expected.every((item, idx) => matchValue(item, actual[idx], bindings));
  }
  const left = expected as Json;
  const right = actual as Json;
  const leftKeys = Object.keys(left).sort();
  const rightKeys = Object.keys(right).sort();
  if (JSON.stringify(leftKeys) !== JSON.stringify(rightKeys)) return false;
  return leftKeys.every((key) => matchValue(left[key], right[key], bindings));
}

/**
 * Strict cassette replay engine modeled on ~/Projects/lgtm/e2e/common/github-cassette.ts.
 * Writes act as ordered barriers; reads may reuse past observations, never future ones.
 */
export class CassetteReplay {
  private readonly phases: Array<{ reads: Exchange[]; write?: Exchange; used: Set<number> }> = [
    { reads: [], used: new Set() },
  ];
  private phase = 0;
  private bindings = new Map<string, string>();

  constructor(cassette: Cassette) {
    for (const fixed of cassette.fixture.runId.match(UUID_RE) ?? []) {
      this.bindings.set(fixed, fixed);
    }
    for (const saved of cassette.exchanges) {
      const response = cassette.responses[saved.response];
      if (!response) throw new Error("Invalid cassette response index");
      const exchange: Exchange = { request: saved.request, response };
      const current = this.phases.at(-1)!;
      if (isReadRequest(exchange.request)) {
        current.reads.push(exchange);
      } else {
        current.write = exchange;
        this.phases.push({ reads: [], used: new Set() });
      }
    }
  }

  respond(request: ExchangeRequest): ExchangeResponse {
    const current = this.phases[this.phase];
    const candidates = isReadRequest(request)
      ? [
          ...current.reads
            .map((exchange, index) => ({ exchange, index }))
            .filter(({ index }) => !current.used.has(index)),
          ...current.reads.map((exchange, index) => ({ exchange, index })).reverse(),
          ...this.phases
            .slice(0, this.phase)
            .reverse()
            .flatMap((past) =>
              [...past.reads].reverse().map((exchange) => ({ exchange, index: -2 })),
            ),
        ]
      : current.write
        ? [{ exchange: current.write, index: -1 }]
        : [];

    for (const { exchange, index } of candidates) {
      const nextBindings = new Map(this.bindings);
      if (!matchValue(exchange.request, request, nextBindings)) continue;
      this.bindings = nextBindings;
      if (index === -1) this.phase++;
      else if (index >= 0) current.used.add(index);
      const rewritten = JSON.stringify(exchange.response).replace(
        UUID_RE,
        (value) => nextBindings.get(value) ?? value,
      );
      return JSON.parse(rewritten) as ExchangeResponse;
    }

    throw new Error(
      `Unrecorded ${request.provider} request in phase ${this.phase}: ${JSON.stringify(request)}\nExpected next write: ${JSON.stringify(current.write?.request ?? null)}. Refresh with mise run e2e:record.`,
    );
  }

  verify(): void {
    const remaining = this.phases.length - 1 - this.phase;
    if (remaining > 0) {
      throw new Error(`Replay omitted ${remaining} recorded write(s)`);
    }
  }
}

export function buildCassette(
  fixture: CassetteFixtureMeta,
  exchanges: Exchange[],
  forbiddenSecrets: string[] = [],
  gitBundle?: string,
): Cassette {
  const responses: ExchangeResponse[] = [];
  const indexByHash = new Map<string, number>();
  const entries: Cassette["exchanges"] = [];

  for (const exchange of exchanges) {
    const cleanRequest = redactValue(exchange.request, forbiddenSecrets) as ExchangeRequest;
    const cleanResponse: ExchangeResponse = {
      status: exchange.response.status,
      headers: sanitizeHeaders(exchange.response.headers),
      body: redactValue(exchange.response.body, forbiddenSecrets) as ExchangeResponse["body"],
    };
    const key = JSON.stringify(cleanResponse);
    let index = indexByHash.get(key);
    if (index === undefined) {
      index = responses.length;
      responses.push(cleanResponse);
      indexByHash.set(key, index);
    }
    entries.push({ request: cleanRequest, response: index });
  }

  const cassette: Cassette = {
    version: 1,
    fixture,
    ...(gitBundle ? { gitBundle } : {}),
    responses,
    exchanges: entries,
  };
  assertNoLeakedSecrets(JSON.stringify(cassette), forbiddenSecrets);
  return cassette;
}
