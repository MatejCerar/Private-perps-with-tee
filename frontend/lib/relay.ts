// Direct-TEE relayer client.
//
// In Direct-TEE mode the on-chain InstructionSender tx still runs (real escrow,
// MetaMask-signed), but the instruction is delivered to the enclave by the local
// relayer instead of the FTDC consensus relay. The relayer returns an action id;
// we poll it for the enclave's signed result. Set NEXT_PUBLIC_RELAYER_URL to the
// relayer's public URL to enable this path; leave it empty for the pure on-chain
// (FCC) path.
import type { ActionResponse } from "./tee";
import type { HlAccount } from "./hl";

export const RELAYER_URL = (process.env.NEXT_PUBLIC_RELAYER_URL ?? "").replace(/\/$/, "");
export const RELAYER_ENABLED = /^https?:\/\//.test(RELAYER_URL);

// Split mode: the deposit is spread across shard accounts the frontend must not
// learn, so account state is read as one aggregate from the relayer (which merges
// the shards inside the enclave) instead of querying HL directly for one address.
export const SPLIT_MODE = (process.env.NEXT_PUBLIC_SPLIT_MODE ?? "") === "true";

const n = (v: unknown): number => {
  const x = Number(v);
  return Number.isFinite(x) ? x : 0;
};

// fetchAggregateAccount reads the user's merged account (balance + positions +
// open orders summed across all shards) from the relayer. Shape matches HlAccount
// so it is a drop-in for fetchAccount().
export async function fetchAggregateAccount(user: string): Promise<HlAccount> {
  const res = await fetch(`${RELAYER_URL}/tee/account?user=${user}`, { cache: "no-store" });
  if (!res.ok) throw new Error(`relayer account failed (${res.status})`);
  const a = (await res.json()) as {
    spotUsdc?: unknown;
    perpAccountValue?: unknown;
    withdrawable?: unknown;
    positions?: unknown[];
    openOrders?: unknown[];
  };
  return {
    spotUsdc: n(a.spotUsdc),
    perpAccountValue: n(a.perpAccountValue),
    withdrawable: n(a.withdrawable),
    positions: (a.positions ?? [])
      .map((p) => p as Record<string, unknown>)
      .filter((p) => n(p.szi) !== 0)
      .map((p) => ({
        coin: String(p.coin),
        szi: n(p.szi),
        entryPx: n(p.entryPx),
        unrealizedPnl: n(p.unrealizedPnl),
        leverage: n(p.leverage),
        marginUsed: n(p.marginUsed),
        positionValue: n(p.positionValue),
        liquidationPx: n(p.liquidationPx),
      })),
    openOrders: (a.openOrders ?? [])
      .map((o) => o as Record<string, unknown>)
      .map((o) => ({
        coin: String(o.coin),
        side: o.side === "sell" ? ("sell" as const) : ("buy" as const),
        sz: n(o.sz),
        limitPx: n(o.limitPx),
        oid: n(o.oid),
      })),
  };
}

async function deliver(path: string, body: unknown): Promise<string> {
  const res = await fetch(`${RELAYER_URL}${path}`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  const json = (await res.json().catch(() => ({}))) as { id?: string; error?: string };
  if (!res.ok || !json.id) {
    throw new Error(json.error || `relayer ${path} failed (${res.status})`);
  }
  return json.id;
}

export const deliverDeposit = (user: string, amountUsd: number) =>
  deliver("/tee/deposit", { user, amountUsd });

export const deliverWithdraw = (user: string, amountUsd: number) =>
  deliver("/tee/withdraw", { user, amountUsd });

export const deliverConfirm = (user: string, amountUsd: number) =>
  deliver("/tee/confirm", { user, amountUsd });

export const deliverOrder = (user: string, order: unknown) =>
  deliver("/tee/order", { user, order });

export const deliverCancel = (user: string, cancel: unknown) =>
  deliver("/tee/cancel", { user, cancel });

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

// Poll the relayer (which proxies the TEE proxy) for a finished result.
// The relayer returns 202 {status:"pending"} until the enclave posts the signed
// result, then 200 with the full ActionResponse.
export async function pollRelayerResult(
  id: string,
  { tries = 60, intervalMs = 2000 }: { tries?: number; intervalMs?: number } = {},
): Promise<ActionResponse> {
  let last: ActionResponse | null = null;
  for (let i = 0; i < tries; i++) {
    const res = await fetch(`${RELAYER_URL}/tee/result/${id}`);
    if (res.status === 200) {
      const body = (await res.json()) as ActionResponse;
      // status 2 == still pending inside the proxy; keep polling.
      if (body?.result && body.result.status !== 2) return body;
      last = body;
    }
    await sleep(intervalMs);
  }
  if (last) return last;
  throw new Error(`relayer result for ${id} not ready after ${tries} tries`);
}
