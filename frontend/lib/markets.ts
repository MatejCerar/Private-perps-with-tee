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

// Only coins that exist on the Hyperliquid *testnet* perp universe (so prices are
// real and orders actually fill). XRP, LINK and SEI are not listed on testnet and
// were removed. szDecimals here is a fallback; it's overridden at runtime from HL
// `meta` (see lib/hl.ts fetchSzDecimals).
export const MARKETS: Market[] = [
  { sym: "BTC", name: "Bitcoin", color: "#f7a54a", px: 4, szDecimals: 5 },
  { sym: "ETH", name: "Ethereum", color: "#7d87a8", px: 3, szDecimals: 4 },
  { sym: "SOL", name: "Solana", color: "#3fe0b0", px: 2, szDecimals: 2 },
  { sym: "BNB", name: "BNB", color: "#f3ba2f", px: 2, szDecimals: 3 },
  { sym: "HYPE", name: "Hyperliquid", color: "#6ee7d0", px: 3, szDecimals: 2 },
  { sym: "AVAX", name: "Avalanche", color: "#e84142", px: 3, szDecimals: 2 },
  { sym: "AAVE", name: "Aave", color: "#b6509e", px: 2, szDecimals: 2 },
  { sym: "MKR", name: "Maker", color: "#1aab9b", px: 2, szDecimals: 4 },
  { sym: "ATOM", name: "Cosmos", color: "#6f7390", px: 3, szDecimals: 2 },
  { sym: "DOGE", name: "Dogecoin", color: "#c2a633", px: 5, szDecimals: 0 },
  { sym: "ARB", name: "Arbitrum", color: "#28a0f0", px: 4, szDecimals: 1 },
  { sym: "OP", name: "Optimism", color: "#ff0420", px: 4, szDecimals: 1 },
  { sym: "SUI", name: "Sui", color: "#4da2ff", px: 4, szDecimals: 1 },
  { sym: "APT", name: "Aptos", color: "#6fd0c6", px: 3, szDecimals: 2 },
  { sym: "TIA", name: "Celestia", color: "#7b2bf9", px: 3, szDecimals: 1 },
  { sym: "INJ", name: "Injective", color: "#00d2ff", px: 3, szDecimals: 2 },
];

export function marketBy(sym: string): Market {
  return MARKETS.find((m) => m.sym === sym) ?? MARKETS[0];
}
