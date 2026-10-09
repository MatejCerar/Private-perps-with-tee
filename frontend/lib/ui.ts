// Design tokens + helpers shared across the UI.
// The design (Hype Accounts.dc.html) is styled almost entirely with inline CSS
// strings. `css()` turns those verbatim strings into React style objects so the
// port stays faithful and the dynamic style expressions (CTA, toggles, chips…)
// read exactly like the source.
import type { CSSProperties } from "react";

export function css(s: string): CSSProperties {
  const out: Record<string, string> = {};
  for (const decl of s.split(";")) {
    const i = decl.indexOf(":");
    if (i === -1) continue;
    const prop = decl.slice(0, i).trim();
    if (!prop) continue;
    const value = decl.slice(i + 1).trim();
    const camel = prop.replace(/-([a-z])/g, (_, c: string) => c.toUpperCase());
    out[camel] = value;
  }
  return out as CSSProperties;
}

// Palette aligned to the official Flare design system. Pink (#e62058) is the
// single primary brand mark and accent; long=green / short=red use the Flare
// semantic palette; surfaces are the Flare neutral blacks.
export const C = {
  bg: "#101010",
  // Flare brand pink is both primary CTA and accent (unified, no coral).
  accent: "#e62058",
  accentText: "#ffffff",
  // Flare brand pink scale (primary CTA / logo mark / active states).
  brand: "#e62058",
  brandSoft: "#f73c68",
  brandSofter: "#fd6f8c",
  green: "#61b854",
  red: "#ee4c45",
  redSoft: "#f2726c",
  text: "#f6f6f6",
  amber: "#fdac59",
  amberText: "#fdac59",
  m1: "#d1d1d1",
  m2: "#a2a2a2",
  m3: "#777777",
  m4: "#414141",
  m5: "#414141",
} as const;

// ---- formatters (mirror the design's num/usd/price/sizeFmt) ----
export function num(v: unknown): number {
  const n = parseFloat(String(v));
  return Number.isFinite(n) ? n : 0;
}

export function usd(n: number): string {
  return (
    "$" +
    Math.abs(n).toLocaleString("en-US", {
      minimumFractionDigits: 2,
      maximumFractionDigits: 2,
    })
  );
}

export function price(n: number): string {
  return (
    "$" +
    n.toLocaleString("en-US", {
      minimumFractionDigits: 2,
      maximumFractionDigits: 2,
    })
  );
}

export function sizeFmt(n: number, decimals: number): string {
  return n.toLocaleString("en-US", {
    minimumFractionDigits: 0,
    maximumFractionDigits: decimals,
  });
}

// 0x1a2…9f — matches the design's truncation.
export function truncAddr(a: string): string {
  return a.length > 8 ? `${a.slice(0, 5)}…${a.slice(-2)}` : a;
}
