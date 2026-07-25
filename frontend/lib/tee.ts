import { hexToString, parseEventLogs, type TransactionReceipt } from "viem";
import { teeInstructionsSentEvent } from "./abi";

// Status codes on ActionResult.Status (tee-node pkg/types/actions.go).
export const STATUS_FAIL = 0;
export const STATUS_OK = 1;
export const STATUS_PENDING = 2;

export function statusLabel(status: number | undefined): string {
  switch (status) {
    case STATUS_OK:
      return "ok";
    case STATUS_FAIL:
      return "failed";
    case STATUS_PENDING:
      return "pending";
    default:
      return "unknown";
  }
}

// Shape returned by the TEE proxy's GET /action/result/{id} (subset we use).
export type ActionResponse = {
  result: {
    id: string;
    status: number;
    log: string;
    opType?: string;
    opCommand?: string;
    version?: string;
    data: `0x${string}`;
  };
  signature?: string;
  proxySignature?: string;
};

// Pull the instructionId out of a mined tx receipt. Mirrors the Go reference
// (tools/pkg/utils/instructions.go), which parses TeeInstructionsSent. We first
// try a proper ABI decode; if the deployed event variant differs, we fall back
// to the raw indexed topic (instructionId is always the 3rd topic).
export function extractInstructionId(receipt: TransactionReceipt): `0x${string}` {
  try {
    const parsed = parseEventLogs({
      abi: [teeInstructionsSentEvent],
      logs: receipt.logs,
    });
    const ev = parsed.find((p) => p.eventName === "TeeInstructionsSent");
    if (ev) {
      return (ev.args as { instructionId: `0x${string}` }).instructionId;
    }
  } catch {
    // fall through to the topic-based fallback
  }
  for (const log of receipt.logs) {
    // topics: [sig, extensionId, instructionId, rewardEpochId]
    if (log.topics.length >= 4 && log.topics[2]) {
      return log.topics[2] as `0x${string}`;
    }
  }
  throw new Error("TeeInstructionsSent event not found in receipt");
}

// Decode ActionResult.Data (hex-encoded JSON bytes) into an object.
export function decodeResultData(data: `0x${string}` | undefined): unknown {
  if (!data || data === "0x") return null;
  const text = hexToString(data);
  try {
    return JSON.parse(text);
  } catch {
    return { raw: text };
  }
}

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

// Pull a human-readable message out of our /api/result error body.
async function errorMessage(res: Response): Promise<string> {
  try {
    const j = (await res.json()) as { error?: string };
    if (j?.error) return j.error;
  } catch {
    /* not JSON */
  }
  return res.statusText || `result endpoint returned ${res.status}`;
}

// Poll our own /api/result/{id} route (which proxies the TEE proxy) until the
// action reaches a terminal status. Long actions return pending (2) first.
//
// Failure handling matters for a live run. The data-provider relay adds ~12s
// before the extension even sees the instruction, and a *reachable* proxy can
// still briefly 5xx while a result settles — closing a position is the slowest
// path (MarketClose does extra info-API round-trips), so it's the most likely to
// hit that window. Those must be ridden out, not treated as fatal. Only a
// definitive config problem should stop the poll early:
//   - 503 NO_PROXY / 400 bad id → definitive, fail now.
//   - 502 (upstream 5xx / unreachable), network blips → transient: keep polling
//     up to a budget, then surface the actual upstream body.
export async function pollResult(
  instructionId: string,
  opts: {
    onUpdate?: (r: ActionResponse) => void;
    signal?: AbortSignal;
    intervalMs?: number;
    maxAttempts?: number;
    hardErrorBudget?: number;
  } = {},
): Promise<ActionResponse> {
  const {
    onUpdate,
    signal,
    intervalMs = 2500,
    maxAttempts = 160,
    hardErrorBudget = 24, // ~60s of consecutive proxy failures before giving up
  } = opts;
  let last: ActionResponse | null = null;
  let hardErrors = 0;
  let lastErr = "";
  for (let i = 0; i < maxAttempts; i++) {
    if (signal?.aborted) throw new Error("aborted");

    // Fetch — only a network/fetch failure throws here; those are transient-ish.
    let res: Response;
    try {
      res = await fetch(`/api/result/${instructionId}`, { signal, cache: "no-store" });
    } catch (e) {
      if (signal?.aborted) throw new Error("aborted");
      lastErr = e instanceof Error ? e.message : String(e);
      if (++hardErrors >= hardErrorBudget) throw new Error(lastErr);
      await sleep(intervalMs);
      continue;
    }

    // Classify the response. Definitive errors propagate immediately.
    if (res.ok) {
      hardErrors = 0;
      try {
        const json = (await res.json()) as ActionResponse;
        last = json;
        onUpdate?.(json);
        if (json?.result?.status !== STATUS_PENDING) return json;
      } catch {
        if (++hardErrors >= hardErrorBudget) throw new Error("malformed result from proxy");
      }
    } else if (res.status === 503 || res.status === 400) {
      // Definitive: our route couldn't even try (proxy URL unset / bad id).
      throw new Error(await errorMessage(res));
    } else {
      // 502 (upstream 5xx / unreachable) — transient. Remember the real cause;
      // give up only after the budget, then surface it.
      lastErr = await errorMessage(res);
      if (++hardErrors >= hardErrorBudget) throw new Error(lastErr);
    }

    await sleep(intervalMs);
  }
  if (last) return last;
  throw new Error(
    lastErr
      ? `Timed out waiting for the instruction result. Last proxy error: ${lastErr}`
      : "Timed out waiting for the instruction result — the relay may be slow or the proxy is unavailable.",
  );
}
