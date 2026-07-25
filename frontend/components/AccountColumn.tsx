"use client";

import { css, C, usd, price, sizeFmt } from "@/lib/ui";
import { marketBy } from "@/lib/markets";
import type { HlPosition, HlOrder } from "@/lib/hl";

export function AccountColumn(props: {
  balance: number;
  available: number;
  inPositions: number;
  everDeposited: boolean;
  positions: HlPosition[];
  orders: HlOrder[];
  mids: Record<string, number>;
  pendingCoins: Set<string>;
  busy: boolean;
  onDeposit: () => void;
  onWithdraw: () => void;
  onClose: (coin: string, frac: number) => void;
  onCancel: (coin: string, oid: number) => void;
}) {
  return (
    <aside style={css("display:flex;flex-direction:column;gap:16px;")}>
      {/* your money */}
      <div
        style={css(
          "border-radius:18px;border:1px solid rgba(255,255,255,0.08);background:#12161f;padding:18px;",
        )}
      >
        <div style={css("display:flex;align-items:center;justify-content:space-between;margin-bottom:12px;")}>
          <span
            style={css(
              "font-size:11px;color:#8a93a6;font-weight:700;letter-spacing:0.08em;text-transform:uppercase;",
            )}
          >
            Your money
          </span>
          <span style={css("display:flex;align-items:center;gap:5px;font-size:10.5px;color:#6f7789;font-weight:600;")}>
            <span
              style={css(
                "width:5px;height:5px;border-radius:50%;background:#2ece6f;animation:hypePulse 2s ease-in-out infinite;",
              )}
            />
            Live
          </span>
        </div>
        <div
          style={css(
            "font-size:34px;font-weight:800;letter-spacing:-0.02em;font-variant-numeric:tabular-nums;margin-bottom:3px;",
          )}
        >
          {usd(props.balance)}
        </div>
        <div style={css("font-size:12px;color:#8a93a6;margin-bottom:14px;")}>
          {props.everDeposited ? "Total equity" : "Deposit to start"}
        </div>
        {props.everDeposited && (
          <div
            style={css(
              "display:flex;gap:16px;padding:10px 13px;border-radius:11px;background:rgba(255,255,255,0.03);border:1px solid rgba(255,255,255,0.06);margin-bottom:16px;",
            )}
          >
            <div style={css("flex:1;")}>
              <div style={css("font-size:10px;color:#8a93a6;font-weight:600;letter-spacing:0.04em;text-transform:uppercase;margin-bottom:3px;")}>
                Available
              </div>
              <div style={css("font-size:14px;font-weight:700;font-variant-numeric:tabular-nums;color:#e9ecf4;")}>
                {usd(props.available)}
              </div>
            </div>
            <div style={css("width:1px;background:rgba(255,255,255,0.08);")} />
            <div style={css("flex:1;")}>
              <div style={css("font-size:10px;color:#8a93a6;font-weight:600;letter-spacing:0.04em;text-transform:uppercase;margin-bottom:3px;")}>
                In positions
              </div>
              <div style={css("font-size:14px;font-weight:700;font-variant-numeric:tabular-nums;color:#e9ecf4;")}>
                {usd(props.inPositions)}
              </div>
            </div>
          </div>
        )}
        <div style={css("display:grid;grid-template-columns:1fr 1fr;gap:8px;")}>
          <button
            onClick={props.onDeposit}
            style={css(
              `padding:11px;border-radius:11px;border:none;background:${C.accent};color:#0b0d13;font-size:13.5px;font-weight:800;cursor:pointer;`,
            )}
          >
            Deposit
          </button>
          <button
            onClick={props.onWithdraw}
            style={css(
              "padding:11px;border-radius:11px;border:1px solid rgba(255,255,255,0.12);background:rgba(255,255,255,0.03);color:#e9ecf4;font-size:13.5px;font-weight:700;cursor:pointer;",
            )}
          >
            Withdraw
          </button>
        </div>
      </div>

      {/* positions */}
      <div
        style={css(
          "border-radius:18px;border:1px solid rgba(255,255,255,0.08);background:#12161f;padding:18px;",
        )}
      >
        <div
          style={css(
            "font-size:11px;color:#8a93a6;font-weight:700;letter-spacing:0.08em;text-transform:uppercase;margin-bottom:14px;",
          )}
        >
          Positions
        </div>
        {props.positions.length > 0 ? (
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
          <div style={css("text-align:center;padding:22px 10px;")}>
            <div style={css("font-size:26px;margin-bottom:8px;opacity:0.7;")}>📈</div>
            <div style={css("font-size:13.5px;font-weight:700;margin-bottom:4px;")}>
              No open positions yet
            </div>
            <div style={css("font-size:12.5px;color:#8a93a6;line-height:1.4;")}>
              Make your first trade with the panel on the left.
            </div>
          </div>
        )}
      </div>

      {/* open orders */}
      {props.orders.length > 0 && (
        <div
          style={css(
            "border-radius:18px;border:1px solid rgba(255,255,255,0.08);background:#12161f;padding:18px;",
          )}
        >
          <div
            style={css(
              "font-size:11px;color:#8a93a6;font-weight:700;letter-spacing:0.08em;text-transform:uppercase;margin-bottom:12px;",
            )}
          >
            Open orders
          </div>
          {props.orders.map((o) => {
            const up = o.side === "buy";
            const badge =
              "padding:3px 8px;border-radius:7px;font-size:10.5px;font-weight:800;color:" +
              (up ? C.green : C.red) +
              ";background:" +
              (up ? "rgba(46,206,111,0.14)" : "rgba(255,93,95,0.14)") +
              ";";
            return (
              <div
                key={o.oid}
                style={css(
                  "display:flex;align-items:center;gap:10px;padding:10px 0;border-top:1px solid rgba(255,255,255,0.06);",
                )}
              >
                <span style={css("font-weight:800;font-size:13px;")}>{o.coin}</span>
                <span style={css(badge)}>{o.side}</span>
                <span style={css("font-size:12px;color:#8a93a6;font-variant-numeric:tabular-nums;")}>
                  {sizeFmt(o.sz, marketBy(o.coin).px)} @ {price(o.limitPx)}
                </span>
                <button
                  onClick={() => props.onCancel(o.coin, o.oid)}
                  disabled={props.busy}
                  style={css(
                    "margin-left:auto;padding:6px 12px;border-radius:9px;border:1px solid rgba(255,255,255,0.12);background:transparent;color:#c8cede;font-size:11.5px;font-weight:700;cursor:pointer;" +
                      (props.busy ? "opacity:0.5;cursor:not-allowed;" : ""),
                  )}
                >
                  Cancel
                </button>
              </div>
            );
          })}
        </div>
      )}
    </aside>
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
  const dirBg = isLong ? "rgba(46,206,111,0.14)" : "rgba(255,93,95,0.14)";

  return (
    <div
      style={css(
        "border-radius:16px;border:1px solid rgba(255,255,255,0.08);background:rgba(255,255,255,0.02);padding:16px;transition:opacity .2s;" +
          (closing ? "opacity:0.55;" : ""),
      )}
    >
      {/* header */}
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
          <span style={css("margin-left:auto;font-size:12px;color:#8a93a6;font-weight:600;")}>
            Closing…
          </span>
        )}
      </div>

      {/* pnl */}
      <div style={css("display:flex;align-items:flex-end;justify-content:space-between;margin-bottom:16px;")}>
        <div>
          <div
            style={css(
              "font-size:11px;color:#8a93a6;font-weight:600;letter-spacing:0.04em;text-transform:uppercase;margin-bottom:3px;",
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

      {/* size / entry / mark */}
      <div
        style={css(
          "display:grid;grid-template-columns:repeat(3,1fr);gap:10px;padding:13px 0;margin-bottom:14px;border-top:1px solid rgba(255,255,255,0.07);border-bottom:1px solid rgba(255,255,255,0.07);",
        )}
      >
        <Stat label="Size" value={sizeFmt(size, cm.px)} sub={p.coin} />
        <Stat label="Entry" value={price(p.entryPx)} />
        <Stat label="Mark" value={price(mark)} />
      </div>

      {/* position value */}
      <div style={css("display:flex;align-items:center;justify-content:space-between;margin-bottom:14px;")}>
        <span style={css("font-size:12.5px;color:#8a93a6;")}>Position value</span>
        <span style={css("font-size:14px;font-weight:700;font-variant-numeric:tabular-nums;")}>
          {usd(p.positionValue)}
        </span>
      </div>

      {/* actions */}
      <div style={css("display:grid;grid-template-columns:1fr 1fr;gap:9px;")}>
        <button
          onClick={() => onClose(p.coin, 0.5)}
          disabled={busy}
          style={css(
            "padding:11px;border-radius:11px;border:1px solid rgba(255,255,255,0.12);background:rgba(255,255,255,0.03);color:#c8cede;font-size:13.5px;font-weight:700;cursor:pointer;" +
              (busy ? "opacity:0.5;cursor:not-allowed;" : ""),
          )}
        >
          Close 50%
        </button>
        <button
          onClick={() => onClose(p.coin, 1)}
          disabled={busy}
          style={css(
            "padding:11px;border-radius:11px;border:none;background:rgba(255,93,95,0.14);color:#ff8082;font-size:13.5px;font-weight:800;cursor:pointer;" +
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
          "font-size:10px;color:#8a93a6;font-weight:600;letter-spacing:0.05em;text-transform:uppercase;margin-bottom:5px;",
        )}
      >
        {label}
      </div>
      <div style={css("font-size:13.5px;font-weight:700;font-variant-numeric:tabular-nums;line-height:1.1;")}>
        {value}
        {sub && <span style={css("color:#8a93a6;font-weight:600;font-size:11px;")}> {sub}</span>}
      </div>
    </div>
  );
}
