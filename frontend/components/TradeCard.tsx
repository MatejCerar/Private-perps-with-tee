"use client";

import { css, C, num, usd, price, sizeFmt } from "@/lib/ui";
import { MARKETS, marketBy } from "@/lib/markets";

export type TradePayload = {
  coin: string;
  isBuy: boolean;
  orderType: "market" | "limit";
  size: number;
  limitPx?: number;
  leverage: number;
  reduceOnly: boolean;
  label: string;
  notional: number;
  pay: number;
};

const LEVELS = [1, 2, 5, 10];

export type ExistingPosition = {
  coin: string;
  isLong: boolean;
  size: number;
  leverage: number;
};

export function TradeCard(props: {
  market: string;
  mids: Record<string, number>;
  marketOpen: boolean;
  onToggleMarket: () => void;
  onPickMarket: (sym: string) => void;
  direction: "long" | "short";
  onLong: () => void;
  onShort: () => void;
  pay: string;
  onPay: (v: string) => void;
  balance: number;
  available: number;
  existingPosition?: ExistingPosition;
  onMax: () => void;
  leverage: number;
  onLev: (v: number) => void;
  advancedOpen: boolean;
  onToggleAdvanced: () => void;
  useLimit: boolean;
  onToggleLimit: () => void;
  limitPrice: string;
  onLimit: (v: string) => void;
  reduceOnly: boolean;
  onToggleReduce: () => void;
  locked: boolean;
  busy: boolean;
  onDeposit: () => void;
  onSubmit: (p: TradePayload) => void;
}) {
  const cm = marketBy(props.market);
  const mid = props.mids[props.market] ?? 0;
  const pay = num(props.pay);
  const notional = pay * props.leverage;
  const size = mid > 0 ? notional / mid : 0;
  const isLong = props.direction === "long";
  const liq = mid > 0 ? (isLong ? mid * (1 - 0.9 / props.leverage) : mid * (1 + 0.9 / props.leverage)) : 0;

  const ep = props.existingPosition;
  const sameDirection = ep && ep.isLong === isLong;

  const validity = (() => {
    if (pay <= 0) return { ok: false, reason: "Enter an amount" };
    if (pay > props.available) return { ok: false, reason: "Not enough available — close positions or deposit" };
    if (notional < 10) return { ok: false, reason: "Increase size or leverage (min ~$10)" };
    if (props.useLimit && !props.limitPrice.trim()) return { ok: false, reason: "Enter a limit price" };
    return { ok: true, reason: "" };
  })();

  const submit = () => {
    if (props.busy || !validity.ok) return;
    const dir = isLong ? "Long" : "Short";
    props.onSubmit({
      coin: props.market,
      isBuy: isLong,
      orderType: props.useLimit ? "limit" : "market",
      size,
      limitPx: props.useLimit ? num(props.limitPrice) : undefined,
      leverage: props.leverage,
      reduceOnly: props.reduceOnly,
      notional,
      pay,
      label: `${dir} ${sizeFmt(size, cm.px)} ${props.market} · ${props.leverage}×`,
    });
  };

  const segBase =
    "display:flex;align-items:center;justify-content:center;gap:6px;padding:11px;border-radius:11px;border:none;font-size:15px;font-weight:800;cursor:pointer;transition:all .15s;";
  const longStyle =
    segBase +
    (isLong
      ? `background:rgba(97,184,84,0.18);color:${C.green};box-shadow:inset 0 0 0 1px rgba(97,184,84,0.4);`
      : "background:transparent;color:#414141;");
  const shortStyle =
    segBase +
    (!isLong
      ? `background:rgba(238,76,69,0.16);color:${C.red};box-shadow:inset 0 0 0 1px rgba(238,76,69,0.4);`
      : "background:transparent;color:#414141;");

  const ctaColor = isLong ? C.green : C.red;
  const enabled = validity.ok && !props.busy;
  const ctaStyle =
    "width:100%;padding:15px;border-radius:14px;border:none;font-size:16px;font-weight:800;transition:all .15s;" +
    (enabled
      ? `background:${ctaColor};color:#1a1a1a;cursor:pointer;box-shadow:0 12px 30px -12px ${ctaColor}88;`
      : "background:rgba(255,255,255,0.05);color:#414141;cursor:not-allowed;");
  const ctaLabel = props.busy
    ? "One action at a time…"
    : validity.ok
      ? ep
        ? sameDirection
          ? `Add to ${ep.isLong ? "Long" : "Short"} ${props.market}`
          : `Reduce ${ep.isLong ? "Long" : "Short"} ${props.market}`
        : `${isLong ? "Long " : "Short "}${props.market}`
      : validity.reason;

  return (
    <section
      style={css(
        "position:relative;border-radius:20px;border:1px solid rgba(255,255,255,0.08);background:linear-gradient(180deg,#1a1a1a,#1a1a1a);box-shadow:0 24px 60px -30px rgba(0,0,0,0.8);overflow:hidden;",
      )}
    >
      <div style={css("padding:20px 20px 22px;")}>
        {/* market selector - kept above the lock overlay (z-index) so the live
            market + mid price are always visible, even before a deposit. */}
        <div
          style={css(
            "position:relative;z-index:16;display:flex;align-items:center;justify-content:space-between;margin-bottom:16px;",
          )}
        >
          <button
            onClick={props.onToggleMarket}
            style={css(
              "display:flex;align-items:center;gap:10px;padding:8px 12px 8px 8px;border-radius:12px;border:1px solid rgba(255,255,255,0.09);background:rgba(255,255,255,0.03);cursor:pointer;",
            )}
          >
            <span
              style={css(
                `width:28px;height:28px;border-radius:50%;background:${cm.color}22;color:${cm.color};display:flex;align-items:center;justify-content:center;font-weight:800;font-size:13px;`,
              )}
            >
              {props.market[0]}
            </span>
            <span style={css("font-weight:800;font-size:16px;color:#f6f6f6;")}>{props.market}</span>
            <span style={css("color:#777777;font-size:11px;")}>▾</span>
          </button>
          <div style={css("text-align:right;")}>
            <div style={css("display:flex;align-items:center;gap:6px;justify-content:flex-end;")}>
              <span
                style={css(
                  "width:6px;height:6px;border-radius:50%;background:#61b854;animation:hypePulse 2s ease-in-out infinite;",
                )}
              />
              <span
                style={css(
                  "font-size:10px;color:#777777;font-weight:600;letter-spacing:0.05em;text-transform:uppercase;",
                )}
              >
                Mid price
              </span>
            </div>
            <div
              style={css(
                "font-size:18px;font-weight:800;color:#f6f6f6;font-variant-numeric:tabular-nums;",
              )}
            >
              {mid > 0 ? price(mid) : "—"}
            </div>
          </div>
          {props.marketOpen && (
            <div
              style={css(
                "position:absolute;top:52px;left:0;z-index:20;width:250px;max-height:340px;overflow-y:auto;padding:6px;border-radius:14px;border:1px solid rgba(255,255,255,0.1);background:#232323;box-shadow:0 20px 44px -18px rgba(0,0,0,0.9);animation:hypeRise 0.16s ease;",
              )}
            >
              {MARKETS.map((m) => (
                <button
                  key={m.sym}
                  onClick={() => props.onPickMarket(m.sym)}
                  style={css(
                    "display:flex;align-items:center;gap:10px;width:100%;padding:9px 10px;border-radius:10px;border:none;background:" +
                      (props.market === m.sym ? "rgba(230,32,88,0.1)" : "transparent") +
                      ";cursor:pointer;text-align:left;",
                  )}
                >
                  <span
                    style={css(
                      `width:26px;height:26px;border-radius:50%;background:${m.color}22;color:${m.color};display:flex;align-items:center;justify-content:center;font-weight:800;font-size:12px;`,
                    )}
                  >
                    {m.sym[0]}
                  </span>
                  <span style={css("display:flex;flex-direction:column;align-items:flex-start;line-height:1.15;")}>
                    <span style={css("font-weight:700;font-size:13.5px;color:#f6f6f6;")}>{m.sym}</span>
                    <span style={css("font-size:11px;color:#777777;")}>{m.name}</span>
                  </span>
                  <span
                    style={css(
                      "margin-left:auto;font-size:13px;font-weight:700;color:#d1d1d1;font-variant-numeric:tabular-nums;",
                    )}
                  >
                    {props.mids[m.sym] ? price(props.mids[m.sym]) : "—"}
                  </span>
                </button>
              ))}
            </div>
          )}
        </div>

        {/* long / short */}
        <div
          style={css(
            "display:grid;grid-template-columns:1fr 1fr;gap:8px;padding:5px;border-radius:14px;background:rgba(0,0,0,0.28);border:1px solid rgba(255,255,255,0.05);margin-bottom:16px;",
          )}
        >
          <button onClick={props.onLong} style={css(longStyle)}>
            ▲ Long
          </button>
          <button onClick={props.onShort} style={css(shortStyle)}>
            ▼ Short
          </button>
        </div>

        {/* you pay */}
        <div
          style={css(
            "border-radius:14px;border:1px solid rgba(255,255,255,0.08);background:rgba(0,0,0,0.22);padding:14px 15px;margin-bottom:10px;",
          )}
        >
          <div style={css("display:flex;justify-content:space-between;align-items:center;margin-bottom:6px;")}>
            <span style={css("font-size:12px;color:#777777;font-weight:600;")}>You pay</span>
            <span style={css("font-size:11.5px;color:#414141;")}>Available {usd(props.available)}</span>
          </div>
          <div style={css("display:flex;align-items:center;gap:10px;")}>
            <input
              value={props.pay}
              onChange={(e) => props.onPay(e.target.value.replace(/[^0-9.]/g, ""))}
              inputMode="decimal"
              placeholder="0"
              style={css(
                "flex:1;min-width:0;border:none;background:transparent;color:#f6f6f6;font-size:30px;font-weight:800;font-family:var(--font-manrope);font-variant-numeric:tabular-nums;padding:0;",
              )}
            />
            <button
              onClick={props.onMax}
              style={css(
                "padding:5px 11px;border-radius:8px;border:1px solid rgba(230,32,88,0.3);background:rgba(230,32,88,0.1);color:#fd6f8c;font-size:11.5px;font-weight:800;cursor:pointer;",
              )}
            >
              MAX
            </button>
            <span style={css("font-size:16px;font-weight:800;color:#777777;")}>USD</span>
          </div>
        </div>

        {/* existing position hint */}
        {ep && (
          <div
            style={css(
              "display:flex;align-items:center;gap:8px;padding:9px 13px;border-radius:11px;margin-bottom:10px;font-size:12px;font-weight:600;line-height:1.4;" +
                (sameDirection
                  ? "background:rgba(230,32,88,0.08);border:1px solid rgba(230,32,88,0.18);color:#fd6f8c;"
                  : "background:rgba(253,172,89,0.08);border:1px solid rgba(253,172,89,0.18);color:#fdac59;"),
            )}
          >
            <span style={css("font-size:14px;")}>{sameDirection ? "＋" : "⚠"}</span>
            <span>
              You have a {ep.leverage}× {ep.isLong ? "Long" : "Short"} {ep.coin} position — this
              trade will {sameDirection ? "increase" : "reduce or flip"} it.
            </span>
          </div>
        )}

        {/* leverage */}
        <div
          style={css(
            "border-radius:14px;border:1px solid rgba(255,255,255,0.08);background:rgba(0,0,0,0.22);padding:14px 15px;margin-bottom:10px;",
          )}
        >
          <div style={css("display:flex;justify-content:space-between;align-items:center;margin-bottom:10px;")}>
            <span style={css("font-size:12px;color:#777777;font-weight:600;")}>Leverage</span>
            <span style={css("font-size:15px;font-weight:800;color:#f6f6f6;")}>{props.leverage}×</span>
          </div>
          <div style={css("display:grid;grid-template-columns:repeat(4,1fr);gap:8px;")}>
            {LEVELS.map((v) => (
              <button
                key={v}
                onClick={() => props.onLev(v)}
                style={css(
                  "padding:9px;border-radius:10px;font-size:13px;font-weight:800;cursor:pointer;transition:all .12s;" +
                    (props.leverage === v
                      ? `border:1px solid ${C.accent};background:rgba(230,32,88,0.14);color:#ffccd5;`
                      : "border:1px solid rgba(255,255,255,0.09);background:rgba(255,255,255,0.02);color:#777777;"),
                )}
              >
                {v}×
              </button>
            ))}
          </div>
        </div>

        {/* exposure readout */}
        <div
          style={css(
            "display:flex;align-items:center;justify-content:space-between;padding:12px 15px;border-radius:14px;background:rgba(230,32,88,0.06);border:1px solid rgba(230,32,88,0.14);margin-bottom:12px;",
          )}
        >
          <div style={css("display:flex;flex-direction:column;gap:3px;")}>
            <span
              style={css(
                "font-size:11px;color:#777777;font-weight:600;letter-spacing:0.03em;text-transform:uppercase;",
              )}
            >
              You get
            </span>
            <span
              style={css(
                "font-size:16px;font-weight:800;color:#f6f6f6;font-variant-numeric:tabular-nums;",
              )}
            >
              {pay > 0 ? `≈ ${sizeFmt(size, cm.px)} ${props.market} · ${usd(notional)}` : "—"}
            </span>
          </div>
          <div style={css("text-align:right;display:flex;flex-direction:column;gap:3px;")}>
            <span style={css("font-size:11.5px;color:#777777;")}>
              est. liq.{" "}
              <span style={css("color:#d1d1d1;font-weight:700;font-variant-numeric:tabular-nums;")}>
                {pay > 0 ? price(liq) : "—"}
              </span>
            </span>
            <span style={css("font-size:11.5px;color:#777777;")}>
              est. fee{" "}
              <span style={css("color:#d1d1d1;font-weight:700;font-variant-numeric:tabular-nums;")}>
                {pay > 0 ? usd(notional * 0.00035) : "—"}
              </span>
            </span>
          </div>
        </div>

        {/* advanced */}
        <button
          onClick={props.onToggleAdvanced}
          style={css(
            "display:flex;align-items:center;gap:6px;width:100%;padding:8px 2px;border:none;background:transparent;color:#777777;font-size:12.5px;font-weight:700;cursor:pointer;",
          )}
        >
          Advanced{" "}
          <span
            style={css(
              "display:inline-block;transition:transform .2s;" +
                (props.advancedOpen ? "transform:rotate(180deg);" : ""),
            )}
          >
            ▾
          </span>
        </button>
        {props.advancedOpen && (
          <div
            style={css(
              "display:flex;flex-direction:column;gap:12px;padding:14px 15px;margin-bottom:12px;border-radius:14px;border:1px solid rgba(255,255,255,0.07);background:rgba(0,0,0,0.2);animation:hypeRise 0.2s ease;",
            )}
          >
            <label style={css("display:flex;align-items:center;justify-content:space-between;cursor:pointer;")}>
              <span style={css("font-size:13px;color:#d1d1d1;font-weight:600;")}>Limit order</span>
              <Toggle on={props.useLimit} onClick={props.onToggleLimit} />
            </label>
            {props.useLimit && (
              <div
                style={css(
                  "display:flex;align-items:center;justify-content:space-between;padding:9px 12px;border-radius:10px;border:1px solid rgba(255,255,255,0.09);background:rgba(0,0,0,0.25);",
                )}
              >
                <span style={css("font-size:12px;color:#777777;")}>Limit price</span>
                <input
                  value={props.limitPrice}
                  onChange={(e) => props.onLimit(e.target.value.replace(/[^0-9.]/g, ""))}
                  inputMode="decimal"
                  placeholder={mid > 0 ? mid.toFixed(2) : "0"}
                  style={css(
                    "width:110px;text-align:right;border:none;background:transparent;color:#f6f6f6;font-size:14px;font-weight:700;font-variant-numeric:tabular-nums;",
                  )}
                />
              </div>
            )}
            <label style={css("display:flex;align-items:center;justify-content:space-between;cursor:pointer;")}>
              <span style={css("font-size:13px;color:#d1d1d1;font-weight:600;")}>Reduce-only</span>
              <Toggle on={props.reduceOnly} onClick={props.onToggleReduce} />
            </label>
            <div style={css("font-size:11.5px;color:#414141;line-height:1.45;")}>
              Margin mode is <strong style={css("color:#777777;")}>cross</strong> (default). Exact
              leverage above 10× and other order types are hidden in this testnet build.
            </div>
          </div>
        )}

        {/* CTA */}
        <button onClick={submit} style={css(ctaStyle)}>
          {ctaLabel}
        </button>
        <div style={css("margin-top:10px;font-size:11.5px;color:#414141;text-align:center;line-height:1.45;")}>
          Fills at market. Hyperliquid needs ~$10+ position size and enough balance.
        </div>
      </div>

      {/* lock overlay until first deposit - starts BELOW the market header so the
          live market + mid price stay visible; only the trade inputs are gated. */}
      {props.locked && (
        <div
          style={css(
            "position:absolute;top:84px;left:0;right:0;bottom:0;z-index:15;display:flex;flex-direction:column;align-items:center;justify-content:center;gap:12px;padding:24px;text-align:center;background:rgba(16,16,16,0.55);backdrop-filter:blur(3px);",
          )}
        >
          <div style={css("font-size:15px;font-weight:800;")}>Deposit to start trading</div>
          <div style={css("font-size:13px;color:#777777;max-width:250px;line-height:1.5;")}>
            Add test dollars and the trade panel unlocks instantly.
          </div>
          <button
            onClick={props.onDeposit}
            style={css(
              `padding:11px 22px;border-radius:12px;border:none;background:${C.accent};color:#ffffff;font-size:14px;font-weight:800;cursor:pointer;`,
            )}
          >
            Deposit test dollars
          </button>
        </div>
      )}
    </section>
  );
}

function Toggle({ on, onClick }: { on: boolean; onClick: () => void }) {
  return (
    <button
      onClick={onClick}
      style={css(
        "position:relative;width:40px;height:23px;border-radius:999px;border:none;cursor:pointer;transition:background .18s;background:" +
          (on ? C.accent : "rgba(255,255,255,0.14)") +
          ";",
      )}
    >
      <span
        style={css(
          "position:absolute;top:3px;left:3px;width:17px;height:17px;border-radius:50%;background:#fff;transition:transform .18s;transform:translateX(" +
            (on ? "17px" : "0") +
            ");",
        )}
      />
    </button>
  );
}
