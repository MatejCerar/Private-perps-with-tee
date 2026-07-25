// The markets the UI exposes. Kept intentionally small (AMM-simple), matching
// the design's coin list. `px` is display precision for prices/sizes; `szDecimals`
// is the Hyperliquid order-size precision — overridden at runtime from HL `meta`
// when available (see lib/hl.ts fetchSzDecimals) so real orders aren't rejected.
export type Market = {
  sym: string;
  name: string;
  color: string;
  px: number;
  szDecimals: number;
};

export const MARKETS: Market[] = [
  { sym: "ETH", name: "Ethereum", color: "#8aa0ff", px: 3, szDecimals: 4 },
  { sym: "BTC", name: "Bitcoin", color: "#f7a54a", px: 4, szDecimals: 5 },
  { sym: "SOL", name: "Solana", color: "#3fe0b0", px: 2, szDecimals: 2 },
  { sym: "HYPE", name: "Hyperliquid", color: "#6ee7d0", px: 2, szDecimals: 2 },
];

export function marketBy(sym: string): Market {
  return MARKETS.find((m) => m.sym === sym) ?? MARKETS[0];
}
