"use client";

import { useState } from "react";
import { css, C, usd, price, sizeFmt } from "@/lib/ui";
import { marketBy } from "@/lib/markets";
import { hlExplorerAddress } from "@/lib/config";
import type { HlPosition, HlOrder } from "@/lib/hl";

// The center-column bottom panel: Positions (PnL + Close) and Open orders
// (Cancel), shown as tabs. The logic mirrors the original AccountColumn exactly;
// only the layout moved into the terminal's center column.
export function PositionsPanel(props: {
  hlAddress?: string;
  positions: HlPosition[];
  orders: HlOrder[];
  mids: Record<string, number>;
  pendingCoins: Set<string>;
  busy: boolean;
  onClose: (coin: string, frac: number) => void;
  onCancel: (coin: string, oid: number) => void;
}) {
  const [tab, setTab] = useState<"positions" | "orders">("positions");

  const tabStyle = (on: boolean) =>
    css(
      "display:flex;align-items:center;gap:7px;padding:8px 12px;border-radius:10px;border:none;background:transparent;cursor:pointer;font-size:12px;font-weight:800;letter-spacing:0.04em;text-transform:uppercase;transition:color .12s;color:" +
        (on ? "#f6f6f6" : "#777777") +
        ";" +
        (on ? "box-shadow:inset 0 -2px 0 " + C.accent + ";" : ""),
    );

  const countBadge = (n: number, on: boolean) =>
    css(
      "min-width:18px;height:18px;padding:0 5px;border-radius:9px;display:inline-flex;align-items:center;justify-content:center;font-size:10.5px;font-weight:800;font-variant-numeric:tabular-nums;background:" +
        (on ? "rgba(230,32,88,0.18)" : "rgba(255,255,255,0.06)") +
        ";color:" +
        (on ? "#fd6f8c" : "#777777") +
        ";",
    );

  return (
    <section
      style={css(
        "display:flex;flex-direction:column;border-radius:16px;border:1px solid rgba(255,255,255,0.08);background:#1a1a1a;overflow:hidden;",
      )}
    >
      {/* tab bar */}
      <div
        style={css(
          "display:flex;align-items:center;gap:4px;padding:8px 12px;border-bottom:1px solid rgba(255,255,255,0.06);",
        )}
      >
        <button onClick={() => setTab("positions")} style={tabStyle(tab === "positions")}>
          Positions
          <span style={countBadge(props.positions.length, tab === "positions")}>
            {props.positions.length}
          </span>
        </button>
        <button onClick={() => setTab("orders")} style={tabStyle(tab === "orders")}>
          Open orders
          <span style={countBadge(props.orders.length, tab === "orders")}>
            {props.orders.length}
          </span>
        </button>
        {props.hlAddress && (
          <a
            href={hlExplorerAddress(props.hlAddress)}
            target="_blank"
            rel="noreferrer"
            style={css("margin-left:auto;font-size:10.5px;font-weight:700;color:#f73c68;text-decoration:none;")}
          >
            Explorer ↗
          </a>
        )}
      </div>

      <div style={css("padding:16px;")}>
        {tab === "positions" ? (
          props.positions.length > 0 ? (
            <div style={css("display:flex;flex-direction:column;gap:10px;")}>
              {props.positions.map((p) => (
                <PositionCard
                  key={p.coin}
                  p={p}
                  mark={props.mids[p.coin] ?? p.entryPx}
                  closing={props.pendingCoins.has(p.coin)}
                  busy={props.busy}
                  onClose={props.onClose}
                />
              ))}
            </div>
          ) : (
            <Empty
              title="No open positions yet"
              sub="Make your first trade with the panel on the right."
            />
          )
        ) : props.orders.length > 0 ? (
          <div style={css("display:flex;flex-direction:column;")}>
            {props.orders.map((o, i) => {
              const up = o.side === "buy";
              const badge =
                "padding:3px 8px;border-radius:7px;font-size:10.5px;font-weight:800;color:" +
                (up ? C.green : C.red) +
                ";background:" +
                (up ? "rgba(97,184,84,0.14)" : "rgba(238,76,69,0.14)") +
                ";";
              return (
                <div
                  key={o.oid}
                  style={css(
                    "display:flex;align-items:center;gap:10px;padding:10px 0;" +
                      (i > 0 ? "border-top:1px solid rgba(255,255,255,0.06);" : ""),
                  )}
                >
                  <span style={css("font-weight:800;font-size:13px;")}>{o.coin}</span>
                  <span style={css(badge)}>{o.side}</span>
                  <span style={css("font-size:12px;color:#777777;font-variant-numeric:tabular-nums;")}>
                    {sizeFmt(o.sz, marketBy(o.coin).px)} @ {price(o.limitPx)}
                  </span>
                  <button
                    onClick={() => props.onCancel(o.coin, o.oid)}
                    disabled={props.busy}
                    style={css(
                      "margin-left:auto;padding:6px 12px;border-radius:9px;border:1px solid rgba(255,255,255,0.12);background:transparent;color:#d1d1d1;font-size:11.5px;font-weight:700;cursor:pointer;" +
                        (props.busy ? "opacity:0.5;cursor:not-allowed;" : ""),
                    )}
                  >
                    Cancel
                  </button>
                </div>
              );
            })}
          </div>
        ) : (
          <Empty title="No open orders" sub="Limit orders you place will appear here." />
        )}
      </div>
    </section>
  );
}

function Empty({ title, sub }: { title: string; sub: string }) {
  return (
    <div style={css("text-align:center;padding:26px 10px;")}>
      <div style={css("font-size:13.5px;font-weight:700;margin-bottom:4px;")}>{title}</div>
      <div style={css("font-size:12.5px;color:#777777;line-height:1.4;")}>{sub}</div>
    </div>
  );
}

function PositionCard({
  p,
  mark,
  closing,
  busy,
  onClose,
}: {
  p: HlPosition;
  mark: number;
  closing: boolean;
  busy: boolean;
  onClose: (coin: string, frac: number) => void;
}) {
  const cm = marketBy(p.coin);
  const size = Math.abs(p.szi);
  const isLong = p.szi >= 0;
  const pnl = p.unrealizedPnl;
  const up = pnl >= 0;
  const col = up ? C.green : C.red;
  const pnlPct = p.marginUsed > 0 ? (pnl / p.marginUsed) * 100 : 0;
  const dirLabel = isLong ? "Long" : "Short";
  const dirCol = isLong ? C.green : C.red;
  const dirBg = isLong ? "rgba(97,184,84,0.14)" : "rgba(238,76,69,0.14)";

  return (
    <div
      style={css(
        "border-radius:16px;border:1px solid rgba(255,255,255,0.08);background:rgba(255,255,255,0.02);padding:16px;transition:opacity .2s;" +
          (closing ? "opacity:0.55;" : ""),
      )}
    >
      <div style={css("display:flex;align-items:center;gap:9px;margin-bottom:14px;")}>
        <span
          style={css(
            `width:30px;height:30px;border-radius:50%;background:${cm.color}22;color:${cm.color};display:flex;align-items:center;justify-content:center;font-weight:800;font-size:14px;`,
          )}
        >
          {p.coin[0]}
        </span>
        <span style={css("font-weight:800;font-size:17px;")}>{p.coin}</span>
        <span
          style={css(
            `padding:4px 10px;border-radius:8px;font-size:12px;font-weight:800;color:${dirCol};background:${dirBg};`,
          )}
        >
          {dirLabel} {p.leverage}×
        </span>
        {closing && (
          <span style={css("margin-left:auto;font-size:12px;color:#777777;font-weight:600;")}>
            Closing…
          </span>
        )}
      </div>

      <div style={css("display:flex;align-items:flex-end;justify-content:space-between;margin-bottom:16px;")}>
        <div>
          <div
            style={css(
              "font-size:11px;color:#777777;font-weight:600;letter-spacing:0.04em;text-transform:uppercase;margin-bottom:3px;",
            )}
          >
            Unrealized PnL
          </div>
          <div
            style={css(
              `font-size:28px;font-weight:800;color:${col};font-variant-numeric:tabular-nums;letter-spacing:-0.01em;line-height:1;`,
            )}
          >
            {(up ? "+" : "−") + usd(pnl).slice(1)} {up ? "▲" : "▼"}
          </div>
        </div>
        <div style={css(`font-size:14px;color:${col};font-weight:800;font-variant-numeric:tabular-nums;`)}>
          {(up ? "+" : "−") + Math.abs(pnlPct).toFixed(1) + "%"}
        </div>
      </div>

      <div
        style={css(
          "display:grid;grid-template-columns:repeat(3,1fr);gap:10px;padding:13px 0;margin-bottom:14px;border-top:1px solid rgba(255,255,255,0.07);border-bottom:1px solid rgba(255,255,255,0.07);",
        )}
      >
        <Stat label="Size" value={sizeFmt(size, cm.px)} sub={p.coin} />
        <Stat label="Entry" value={price(p.entryPx)} />
        <Stat label="Mark" value={price(mark)} />
      </div>

      <div style={css("display:flex;align-items:center;justify-content:space-between;margin-bottom:14px;")}>
        <span style={css("font-size:12.5px;color:#777777;")}>Position value</span>
        <span style={css("font-size:14px;font-weight:700;font-variant-numeric:tabular-nums;")}>
          {usd(p.positionValue)}
        </span>
      </div>

      <div style={css("display:grid;grid-template-columns:1fr 1fr;gap:9px;")}>
        <button
          onClick={() => onClose(p.coin, 0.5)}
          disabled={busy}
          style={css(
            "padding:11px;border-radius:11px;border:1px solid rgba(255,255,255,0.12);background:rgba(255,255,255,0.03);color:#d1d1d1;font-size:13.5px;font-weight:700;cursor:pointer;" +
              (busy ? "opacity:0.5;cursor:not-allowed;" : ""),
          )}
        >
          Close 50%
        </button>
        <button
          onClick={() => onClose(p.coin, 1)}
          disabled={busy}
          style={css(
            "padding:11px;border-radius:11px;border:none;background:rgba(238,76,69,0.14);color:#f2726c;font-size:13.5px;font-weight:800;cursor:pointer;" +
              (busy ? "opacity:0.5;cursor:not-allowed;" : ""),
          )}
        >
          Close
        </button>
      </div>
    </div>
  );
}

function Stat({ label, value, sub }: { label: string; value: string; sub?: string }) {
  return (
    <div>
      <div
        style={css(
          "font-size:10px;color:#777777;font-weight:600;letter-spacing:0.05em;text-transform:uppercase;margin-bottom:5px;",
        )}
      >
        {label}
      </div>
      <div style={css("font-size:13.5px;font-weight:700;font-variant-numeric:tabular-nums;line-height:1.1;")}>
        {value}
        {sub && <span style={css("color:#777777;font-weight:600;font-size:11px;")}> {sub}</span>}
      </div>
    </div>
  );
}
