// Public API -----------------------------------------------------------------

// eslint-disable-next-line @typescript-eslint/no-explicit-any
export type Json = Record<string, any>;

export type E2EMode = "replay" | "record" | "live";

export function e2eMode(): E2EMode {
  const raw = (process.env.PREMISE_E2E_MODE ?? "replay").trim().toLowerCase();
  if (raw === "replay" || raw === "record" || raw === "live") return raw;
  throw new Error(`Invalid PREMISE_E2E_MODE "${raw}": expected replay, record, or live`);
}

export function isReplayMode(): boolean {
  return e2eMode() === "replay";
}

export function isLiveOrRecordMode(): boolean {
  const mode = e2eMode();
  return mode === "live" || mode === "record";
}

export function shouldKeepRemote(testFullName: string): boolean {
  const filter = (process.env.PREMISE_E2E_KEEP_REMOTE ?? "").trim();
  if (!filter || filter === "0" || filter === "false") return false;
  if (filter === "1" || filter === "true" || filter === "all") return true;
  try {
    return new RegExp(filter, "i").test(testFullName);
  } catch {
    return testFullName.toLowerCase().includes(filter.toLowerCase());
  }
}
