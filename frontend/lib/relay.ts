// Direct-TEE relayer client.
//
// In Direct-TEE mode the on-chain InstructionSender tx still runs (real escrow,
// MetaMask-signed), but the instruction is delivered to the enclave by the local
// relayer instead of the FTDC consensus relay. The relayer returns an action id;
// we poll it for the enclave's signed result. Set NEXT_PUBLIC_RELAYER_URL to the
// relayer's public URL to enable this path; leave it empty for the pure on-chain
// (FCC) path.
import type { ActionResponse } from "./tee";

export const RELAYER_URL = (process.env.NEXT_PUBLIC_RELAYER_URL ?? "").replace(/\/$/, "");
export const RELAYER_ENABLED = /^https?:\/\//.test(RELAYER_URL);

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
