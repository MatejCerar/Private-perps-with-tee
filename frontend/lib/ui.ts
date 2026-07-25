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

// Palette lifted from the design.
export const C = {
  bg: "#080a0f",
  accent: "#7c8cff",
  accentText: "#0b0d13",
  green: "#2ece6f",
  red: "#ff5d5f",
  redSoft: "#ff8082",
  text: "#e9ecf4",
  amber: "#e3ad46",
  amberText: "#e8c885",
  m1: "#c8cede",
  m2: "#9aa3b6",
  m3: "#8a93a6",
  m4: "#6f7789",
  m5: "#5f6779",
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
