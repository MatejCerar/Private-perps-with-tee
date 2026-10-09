"use client";

import { useMemo, useState } from "react";
import { css, C, price } from "@/lib/ui";
import type { Market } from "@/lib/markets";

// Scrollable, searchable list of all markets with live mid prices. Clicking a row
// selects the market (drives the shared selected-market state). The active market
// is highlighted. Flare dark styling, consistent with the rest of the app.
export function MarketsSidebar({
  markets,
  mids,
  selected,
  onSelect,
}: {
  markets: Market[];
  mids: Record<string, number>;
  selected: string;
  onSelect: (sym: string) => void;
}) {
  const [query, setQuery] = useState("");

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return markets;
    return markets.filter(
      (m) => m.sym.toLowerCase().includes(q) || m.name.toLowerCase().includes(q),
    );
  }, [markets, query]);

  return (
    <aside
      style={css(
        "display:flex;flex-direction:column;border-radius:16px;border:1px solid rgba(255,255,255,0.08);background:#1a1a1a;overflow:hidden;height:100%;min-height:0;",
      )}
    >
      {/* header + search */}
      <div style={css("padding:14px 14px 10px;border-bottom:1px solid rgba(255,255,255,0.06);")}>
        <div
          style={css(
            "font-size:11px;color:#777777;font-weight:700;letter-spacing:0.08em;text-transform:uppercase;margin-bottom:10px;",
          )}
        >
          Markets
        </div>
        <div
          style={css(
            "display:flex;align-items:center;gap:8px;padding:8px 11px;border-radius:10px;border:1px solid rgba(255,255,255,0.09);background:rgba(0,0,0,0.25);",
          )}
        >
          <span style={css("color:#414141;font-size:13px;")}>⌕</span>
          <input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Search markets"
            style={css(
              "flex:1;min-width:0;border:none;background:transparent;color:#f6f6f6;font-size:13px;font-weight:600;padding:0;",
            )}
          />
        </div>
      </div>

      {/* list */}
      <div style={css("flex:1;min-height:0;overflow-y:auto;padding:6px;")}>
        {filtered.length === 0 && (
          <div style={css("padding:22px 10px;text-align:center;color:#414141;font-size:12.5px;")}>
            No markets match.
          </div>
        )}
        {filtered.map((m) => {
          const on = m.sym === selected;
          const mid = mids[m.sym];
          return (
            <button
              key={m.sym}
              onClick={() => onSelect(m.sym)}
              style={css(
                "display:flex;align-items:center;gap:10px;width:100%;padding:9px 10px;border-radius:10px;border:none;cursor:pointer;text-align:left;transition:background .12s;margin-bottom:2px;background:" +
                  (on ? "rgba(230,32,88,0.12)" : "transparent") +
                  ";" +
                  (on ? `box-shadow:inset 0 0 0 1px ${C.accent}55;` : ""),
              )}
            >
              <span
                style={css(
                  `width:26px;height:26px;border-radius:50%;background:${m.color}22;color:${m.color};display:flex;align-items:center;justify-content:center;font-weight:800;font-size:12px;flex-shrink:0;`,
                )}
              >
                {m.sym[0]}
              </span>
              <span style={css("display:flex;flex-direction:column;align-items:flex-start;line-height:1.15;min-width:0;")}>
                <span
                  style={css(
                    "font-weight:800;font-size:13.5px;color:" + (on ? "#fd6f8c" : "#f6f6f6") + ";",
                  )}
                >
                  {m.sym}
                </span>
                <span style={css("font-size:11px;color:#777777;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;max-width:110px;")}>
                  {m.name}
                </span>
              </span>
              <span
                style={css(
                  "margin-left:auto;font-size:13px;font-weight:700;font-variant-numeric:tabular-nums;color:" +
                    (mid ? "#d1d1d1" : "#414141") +
                    ";",
                )}
              >
                {mid ? price(mid) : "—"}
              </span>
            </button>
          );
        })}
      </div>
    </aside>
  );
}
