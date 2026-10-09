"use client";

import { useEffect, useMemo, useRef, useState, type MouseEvent } from "react";
import { css, C, price } from "@/lib/ui";
import { marketBy, MARKETS } from "@/lib/markets";
import { fetchCandles, type Candle } from "@/lib/hl";

// Timeframes mirror a Hyperliquid chart: each picks an HL candle interval + a
// lookback window. Change/high/low are computed over the selected window.
const FRAMES: { key: string; label: string; interval: string; lookbackMs: number }[] = [
  { key: "1D", label: "1D", interval: "15m", lookbackMs: 24 * 3600_000 },
  { key: "1W", label: "1W", interval: "1h", lookbackMs: 7 * 24 * 3600_000 },
  { key: "1M", label: "1M", interval: "4h", lookbackMs: 30 * 24 * 3600_000 },
];

// The live market panel: coin, mark price, window change, high/low and a chart.
// Prices are public market data, so this renders even before a wallet connects -
// a trading app should show the market the moment it opens, like Hyperliquid.
export function MarketChart({
  market,
  mid,
  onPickMarket,
}: {
  market: string;
  mid: number;
  onPickMarket: (sym: string) => void;
}) {
  const cm = marketBy(market);
  const [frameKey, setFrameKey] = useState("1D");
  const frame = FRAMES.find((f) => f.key === frameKey) ?? FRAMES[0];
  const [candles, setCandles] = useState<Candle[]>([]);
  const [hover, setHover] = useState<number | null>(null);

  useEffect(() => {
    let alive = true;
    const load = () =>
      fetchCandles(market, frame.interval, frame.lookbackMs, Date.now())
        .then((c) => alive && setCandles(c))
        .catch(() => {});
    load();
    const t = setInterval(load, 15000);
    return () => {
      alive = false;
      clearInterval(t);
    };
  }, [market, frame.interval, frame.lookbackMs]);

  // Live mid overrides the last close so the chart tip tracks the ticker.
  const closes = useMemo(() => {
    const cs = candles.map((k) => k.c);
    if (cs.length && mid > 0) cs[cs.length - 1] = mid;
    return cs;
  }, [candles, mid]);

  const open = closes[0] ?? 0;
  const last = mid > 0 ? mid : closes[closes.length - 1] ?? 0;
  const change = open > 0 ? ((last - open) / open) * 100 : 0;
  const up = change >= 0;
  const hi = candles.length ? Math.max(...candles.map((k) => k.h)) : 0;
  const lo = candles.length ? Math.min(...candles.map((k) => k.l)) : 0;
  const lineColor = up ? C.green : C.red;

  const hovered = hover != null ? closes[hover] : undefined;

  return (
    <section
      style={css(
        "border-radius:20px;border:1px solid rgba(255,255,255,0.08);background:linear-gradient(180deg,#1a1a1a,#1a1a1a);box-shadow:0 24px 60px -30px rgba(0,0,0,0.8);padding:18px 20px 14px;margin-bottom:22px;",
      )}
    >
      {/* top row: coin tabs + mark price + change */}
      <div
        style={css(
          "display:flex;align-items:flex-start;justify-content:space-between;gap:16px;flex-wrap:wrap;",
        )}
      >
        <div style={css("display:flex;align-items:center;gap:8px;")}>
          {MARKETS.map((m) => {
            const on = m.sym === market;
            return (
              <button
                key={m.sym}
                onClick={() => onPickMarket(m.sym)}
                style={css(
                  "display:flex;align-items:center;gap:7px;padding:7px 11px;border-radius:11px;cursor:pointer;transition:all .14s;font-weight:800;font-size:13px;" +
                    (on
                      ? "border:1px solid rgba(255,255,255,0.14);background:rgba(255,255,255,0.05);color:#f6f6f6;"
                      : "border:1px solid transparent;background:transparent;color:#414141;"),
                )}
              >
                <span
                  style={css(
                    `width:20px;height:20px;border-radius:50%;background:${m.color}22;color:${m.color};display:flex;align-items:center;justify-content:center;font-size:10px;font-weight:800;`,
                  )}
                >
                  {m.sym[0]}
                </span>
                {m.sym}
              </button>
            );
          })}
        </div>

        <div style={css("text-align:right;")}>
          <div
            style={css(
              "font-size:26px;font-weight:800;color:#f6f6f6;font-variant-numeric:tabular-nums;line-height:1.1;",
            )}
          >
            {hovered ? price(hovered) : last > 0 ? price(last) : "—"}
          </div>
          <div
            style={css(
              `font-size:13px;font-weight:800;font-variant-numeric:tabular-nums;color:${up ? C.green : C.red};`,
            )}
          >
            {last > 0 ? `${up ? "▲" : "▼"} ${up ? "+" : ""}${change.toFixed(2)}% · ${frame.label}` : ""}
          </div>
        </div>
      </div>

      {/* stats strip */}
      <div
        style={css(
          "display:flex;align-items:center;gap:20px;margin-top:10px;font-size:11.5px;color:#777777;",
        )}
      >
        <Stat label={`${frame.label} High`} value={hi > 0 ? price(hi) : "—"} />
        <Stat label={`${frame.label} Low`} value={lo > 0 ? price(lo) : "—"} />
        <Stat label="Coin" value={cm.name} />
        <div style={css("margin-left:auto;display:flex;gap:6px;")}>
          {FRAMES.map((f) => (
            <button
              key={f.key}
              onClick={() => setFrameKey(f.key)}
              style={css(
                "padding:4px 10px;border-radius:8px;font-size:11px;font-weight:800;cursor:pointer;transition:all .12s;" +
                  (f.key === frameKey
                    ? `border:1px solid ${C.brand};background:rgba(230,32,88,0.14);color:${C.brandSofter};`
                    : "border:1px solid rgba(255,255,255,0.09);background:transparent;color:#777777;"),
              )}
            >
              {f.label}
            </button>
          ))}
        </div>
      </div>

      {/* chart */}
      <Sparkline closes={closes} color={lineColor} onHover={setHover} />
    </section>
  );
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div style={css("display:flex;flex-direction:column;gap:1px;")}>
      <span style={css("font-size:10px;letter-spacing:0.04em;text-transform:uppercase;color:#414141;")}>
        {label}
      </span>
      <span style={css("font-size:12.5px;font-weight:700;color:#d1d1d1;font-variant-numeric:tabular-nums;")}>
        {value}
      </span>
    </div>
  );
}

// A lightweight SVG area chart - no chart lib, just a path over the closes. Hover
// reports the index back so the header can show the price at the cursor.
function Sparkline({
  closes,
  color,
  onHover,
}: {
  closes: number[];
  color: string;
  onHover: (i: number | null) => void;
}) {
  const ref = useRef<SVGSVGElement | null>(null);
  const W = 1000;
  const H = 220;
  const pad = 6;

  if (closes.length < 2) {
    return (
      <div
        style={css(
          "height:220px;margin-top:10px;display:flex;align-items:center;justify-content:center;color:#414141;font-size:12px;",
        )}
      >
        Loading market…
      </div>
    );
  }

  const min = Math.min(...closes);
  const max = Math.max(...closes);
  const span = max - min || 1;
  const x = (i: number) => pad + (i / (closes.length - 1)) * (W - 2 * pad);
  const y = (v: number) => pad + (1 - (v - min) / span) * (H - 2 * pad);

  const line = closes.map((v, i) => `${i === 0 ? "M" : "L"}${x(i).toFixed(1)},${y(v).toFixed(1)}`).join(" ");
  const area = `${line} L${x(closes.length - 1).toFixed(1)},${H} L${x(0).toFixed(1)},${H} Z`;

  const onMove = (e: MouseEvent<SVGSVGElement>) => {
    const svg = ref.current;
    if (!svg) return;
    const rect = svg.getBoundingClientRect();
    const frac = (e.clientX - rect.left) / rect.width;
    const i = Math.max(0, Math.min(closes.length - 1, Math.round(frac * (closes.length - 1))));
    onHover(i);
  };

  return (
    <svg
      ref={ref}
      viewBox={`0 0 ${W} ${H}`}
      preserveAspectRatio="none"
      onMouseMove={onMove}
      onMouseLeave={() => onHover(null)}
      style={css("width:100%;height:220px;margin-top:10px;display:block;cursor:crosshair;")}
    >
      <defs>
        <linearGradient id="mcFill" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stopColor={color} stopOpacity="0.22" />
          <stop offset="100%" stopColor={color} stopOpacity="0" />
        </linearGradient>
      </defs>
      <path d={area} fill="url(#mcFill)" />
      <path d={line} fill="none" stroke={color} strokeWidth="2" vectorEffect="non-scaling-stroke" />
    </svg>
  );
}
