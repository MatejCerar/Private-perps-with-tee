// Client for reading the derived Hyperliquid account state (via /api/hl, a
// same-origin server proxy to HL's public info API). Everything here is a read;
// writes go through the on-chain instruction pipeline (lib/tee.ts).

export type HlPosition = {
  coin: string;
  szi: number; // signed size: + long, - short
  entryPx: number;
  unrealizedPnl: number;
  leverage: number;
  marginUsed: number; // collateral backing the position (for PnL %)
  positionValue: number; // notional
  liquidationPx: number;
};

export type HlOrder = {
  coin: string;
  side: "buy" | "sell";
  sz: number;
  limitPx: number;
  oid: number;
};

export type HlAccount = {
  spotUsdc: number;
  perpAccountValue: number;
  withdrawable: number;
  positions: HlPosition[];
  openOrders: HlOrder[];
};

async function info(body: object): Promise<unknown> {
  const r = await fetch("/api/hl", {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify(body),
    cache: "no-store",
  });
  if (!r.ok) throw new Error(`HL info ${r.status}`);
  return r.json();
}

function num(v: unknown): number {
  const n = Number(v);
  return Number.isFinite(n) ? n : 0;
}

export async function fetchAccount(address: string): Promise<HlAccount> {
  const [perp, spot, orders] = (await Promise.all([
    info({ type: "clearinghouseState", user: address }),
    info({ type: "spotClearinghouseState", user: address }),
    info({ type: "openOrders", user: address }),
  ])) as [any, any, any];

  const spotUsdc = num(
    (spot?.balances ?? []).find((b: any) => b.coin === "USDC")?.total,
  );
  const perpAccountValue = num(perp?.marginSummary?.accountValue);
  const withdrawable = num(perp?.withdrawable);

  const positions: HlPosition[] = (perp?.assetPositions ?? [])
    .map((p: any) => p.position)
    .filter((p: any) => p && num(p.szi) !== 0)
    .map((p: any) => ({
      coin: String(p.coin),
      szi: num(p.szi),
      entryPx: num(p.entryPx),
      unrealizedPnl: num(p.unrealizedPnl),
      leverage: num(p.leverage?.value ?? p.leverage),
      marginUsed: num(p.marginUsed),
      positionValue: num(p.positionValue),
      liquidationPx: num(p.liquidationPx),
    }));

  const openOrders: HlOrder[] = (orders ?? []).map((o: any) => ({
    coin: String(o.coin),
    side: o.side === "B" ? "buy" : "sell",
    sz: num(o.sz),
    limitPx: num(o.limitPx),
    oid: num(o.oid),
  }));

  return { spotUsdc, perpAccountValue, withdrawable, positions, openOrders };
}

// Live mid prices, keyed by coin. Only string coin symbols are kept (HL also
// returns synthetic @-indexed spot pairs we don't surface).
export async function fetchMids(): Promise<Record<string, number>> {
  const raw = (await info({ type: "allMids" })) as Record<string, string>;
  const out: Record<string, number> = {};
  for (const [coin, px] of Object.entries(raw ?? {})) {
    if (coin.startsWith("@")) continue;
    out[coin] = num(px);
  }
  return out;
}

export type Candle = { t: number; o: number; h: number; l: number; c: number; v: number };

// Candle history for the selected coin, used to draw the market chart. `interval`
// is an HL candle interval ("5m", "15m", "1h"…); `lookbackMs` sets the window.
export async function fetchCandles(
  coin: string,
  interval: string,
  lookbackMs: number,
  now: number,
): Promise<Candle[]> {
  const raw = (await info({
    type: "candleSnapshot",
    req: { coin, interval, startTime: now - lookbackMs, endTime: now },
  })) as any[];
  return (raw ?? []).map((k) => ({
    t: num(k.t),
    o: num(k.o),
    h: num(k.h),
    l: num(k.l),
    c: num(k.c),
    v: num(k.v),
  }));
}

// Order-size precision per coin, from HL's perp meta. Used to round order sizes
// so Hyperliquid doesn't reject them. Best-effort — callers fall back to the
// static table in lib/markets.ts.
export async function fetchSzDecimals(): Promise<Record<string, number>> {
  const meta = (await info({ type: "meta" })) as {
    universe?: { name: string; szDecimals: number }[];
  };
  const out: Record<string, number> = {};
  for (const u of meta?.universe ?? []) {
    if (u?.name) out[u.name] = num(u.szDecimals);
  }
  return out;
}
