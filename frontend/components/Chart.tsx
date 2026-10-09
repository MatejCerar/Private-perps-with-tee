"use client";

import { useEffect, useRef, useState } from "react";
import { css, C, price } from "@/lib/ui";
import { marketBy } from "@/lib/markets";
import { fetchCandles, type Candle } from "@/lib/hl";

// Interval buttons map to an HL candle interval + a lookback window. Enough
// candles to fill the width without overcrowding.
const INTERVALS: { key: string; label: string; interval: string; lookbackMs: number }[] = [
  { key: "1m", label: "1m", interval: "1m", lookbackMs: 2 * 3600_000 },
  { key: "15m", label: "15m", interval: "15m", lookbackMs: 24 * 3600_000 },
  { key: "1h", label: "1h", interval: "1h", lookbackMs: 7 * 24 * 3600_000 },
  { key: "1d", label: "1d", interval: "1d", lookbackMs: 120 * 24 * 3600_000 },
];

// Dependency-free candlestick chart on a <canvas>. Draws green/red candles, a
// faint grid, axis price labels and the latest price as a right-edge line+label.
// Auto-refreshes every ~10s and whenever coin/interval changes.
export function Chart({ market, mid }: { market: string; mid: number }) {
  const cm = marketBy(market);
  const [intervalKey, setIntervalKey] = useState("15m");
  const iv = INTERVALS.find((f) => f.key === intervalKey) ?? INTERVALS[1];
  const [candles, setCandles] = useState<Candle[]>([]);
  const [loading, setLoading] = useState(true);

  const wrapRef = useRef<HTMLDivElement | null>(null);
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const [size, setSize] = useState({ w: 800, h: 320 });

  // Load candles (coin/interval change + periodic refresh).
  useEffect(() => {
    let alive = true;
    setLoading(true);
    const load = () =>
      fetchCandles(market, iv.interval, iv.lookbackMs, Date.now())
        .then((c) => {
          if (!alive) return;
          setCandles(c);
          setLoading(false);
        })
        .catch(() => alive && setLoading(false));
    load();
    const t = setInterval(load, 10000);
    return () => {
      alive = false;
      clearInterval(t);
    };
  }, [market, iv.interval, iv.lookbackMs]);

  // Track the container size so the canvas stays crisp on resize.
  useEffect(() => {
    const el = wrapRef.current;
    if (!el) return;
    const apply = () =>
      setSize({ w: el.clientWidth, h: el.clientHeight || 320 });
    apply();
    const ro = new ResizeObserver(apply);
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  // Live mid overrides the last close so the latest candle tracks the ticker.
  const drawCandles =
    candles.length && mid > 0
      ? candles.map((k, i) =>
          i === candles.length - 1
            ? { ...k, c: mid, h: Math.max(k.h, mid), l: Math.min(k.l, mid) }
            : k,
        )
      : candles;

  const last =
    mid > 0 ? mid : drawCandles.length ? drawCandles[drawCandles.length - 1].c : 0;
  const open = drawCandles.length ? drawCandles[0].o : 0;
  const change = open > 0 ? ((last - open) / open) * 100 : 0;
  const up = change >= 0;

  // Paint whenever data or size changes.
  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const dpr = typeof window !== "undefined" ? window.devicePixelRatio || 1 : 1;
    const W = size.w;
    const H = size.h;
    canvas.width = Math.max(1, Math.floor(W * dpr));
    canvas.height = Math.max(1, Math.floor(H * dpr));
    canvas.style.width = `${W}px`;
    canvas.style.height = `${H}px`;
    const ctx = canvas.getContext("2d");
    if (!ctx) return;
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    ctx.clearRect(0, 0, W, H);

    if (drawCandles.length < 2) return;

    const padR = 62; // room for right-edge price axis labels
    const padT = 10;
    const padB = 22;
    const plotW = W - padR;
    const plotH = H - padT - padB;

    let hi = -Infinity;
    let lo = Infinity;
    for (const k of drawCandles) {
      if (k.h > hi) hi = k.h;
      if (k.l < lo) lo = k.l;
    }
    const span = hi - lo || 1;
    const pad = span * 0.08;
    hi += pad;
    lo -= pad;
    const range = hi - lo || 1;
    const y = (v: number) => padT + (1 - (v - lo) / range) * plotH;

    // faint horizontal grid + price axis labels
    ctx.font =
      "10px var(--font-manrope), system-ui, sans-serif";
    ctx.textBaseline = "middle";
    const rows = 5;
    for (let r = 0; r <= rows; r++) {
      const v = lo + (range * r) / rows;
      const yy = y(v);
      ctx.strokeStyle = "rgba(255,255,255,0.05)";
      ctx.lineWidth = 1;
      ctx.beginPath();
      ctx.moveTo(0, yy);
      ctx.lineTo(plotW, yy);
      ctx.stroke();
      ctx.fillStyle = "#414141";
      ctx.textAlign = "left";
      ctx.fillText(price(v), plotW + 6, yy);
    }

    const n = drawCandles.length;
    const slot = plotW / n;
    const bodyW = Math.max(1, Math.min(slot * 0.68, 14));

    for (let i = 0; i < n; i++) {
      const k = drawCandles[i];
      const cx = i * slot + slot / 2;
      const kUp = k.c >= k.o;
      const col = kUp ? C.green : C.red;
      // wick
      ctx.strokeStyle = col;
      ctx.lineWidth = 1;
      ctx.beginPath();
      ctx.moveTo(cx, y(k.h));
      ctx.lineTo(cx, y(k.l));
      ctx.stroke();
      // body
      const yo = y(k.o);
      const yc = y(k.c);
      const top = Math.min(yo, yc);
      const h = Math.max(1, Math.abs(yc - yo));
      ctx.fillStyle = col;
      ctx.fillRect(cx - bodyW / 2, top, bodyW, h);
    }

    // latest price line + label on the right edge
    const lastY = y(last);
    const lineCol = up ? C.green : C.red;
    ctx.save();
    ctx.setLineDash([4, 4]);
    ctx.strokeStyle = lineCol;
    ctx.lineWidth = 1;
    ctx.beginPath();
    ctx.moveTo(0, lastY);
    ctx.lineTo(plotW, lastY);
    ctx.stroke();
    ctx.restore();

    const label = price(last);
    ctx.font = "700 10px var(--font-manrope), system-ui, sans-serif";
    const tw = ctx.measureText(label).width;
    const bx = plotW + 2;
    const bh = 15;
    ctx.fillStyle = lineCol;
    ctx.fillRect(bx, lastY - bh / 2, Math.min(padR - 4, tw + 10), bh);
    ctx.fillStyle = "#1a1a1a";
    ctx.textAlign = "left";
    ctx.textBaseline = "middle";
    ctx.fillText(label, bx + 5, lastY + 0.5);
  }, [drawCandles, size, last, up]);

  return (
    <section
      style={css(
        "display:flex;flex-direction:column;border-radius:16px;border:1px solid rgba(255,255,255,0.08);background:linear-gradient(180deg,#1a1a1a,#1a1a1a);overflow:hidden;",
      )}
    >
      {/* market header: coin, live mid, change, interval buttons */}
      <div
        style={css(
          "display:flex;align-items:center;gap:14px;padding:13px 16px;border-bottom:1px solid rgba(255,255,255,0.06);flex-wrap:wrap;",
        )}
      >
        <div style={css("display:flex;align-items:center;gap:9px;")}>
          <span
            style={css(
              `width:28px;height:28px;border-radius:50%;background:${cm.color}22;color:${cm.color};display:flex;align-items:center;justify-content:center;font-weight:800;font-size:13px;`,
            )}
          >
            {market[0]}
          </span>
          <div style={css("display:flex;flex-direction:column;line-height:1.1;")}>
            <span style={css("font-weight:800;font-size:16px;color:#f6f6f6;")}>{market}</span>
            <span style={css("font-size:11px;color:#777777;")}>{cm.name}</span>
          </div>
        </div>

        <div style={css("display:flex;flex-direction:column;align-items:flex-start;line-height:1.1;")}>
          <span
            style={css(
              "font-size:20px;font-weight:800;color:#f6f6f6;font-variant-numeric:tabular-nums;",
            )}
          >
            {last > 0 ? price(last) : "—"}
          </span>
          <span
            style={css(
              `font-size:12px;font-weight:800;font-variant-numeric:tabular-nums;color:${up ? C.green : C.red};`,
            )}
          >
            {last > 0 ? `${up ? "▲" : "▼"} ${up ? "+" : ""}${change.toFixed(2)}%` : ""}
          </span>
        </div>

        <div style={css("margin-left:auto;display:flex;gap:6px;")}>
          {INTERVALS.map((f) => (
            <button
              key={f.key}
              onClick={() => setIntervalKey(f.key)}
              style={css(
                "padding:5px 11px;border-radius:9px;font-size:12px;font-weight:800;cursor:pointer;transition:all .12s;" +
                  (f.key === intervalKey
                    ? `border:1px solid ${C.accent};background:rgba(230,32,88,0.14);color:#fd6f8c;`
                    : "border:1px solid rgba(255,255,255,0.09);background:transparent;color:#777777;"),
              )}
            >
              {f.label}
            </button>
          ))}
        </div>
      </div>

      {/* canvas */}
      <div
        ref={wrapRef}
        style={css("position:relative;width:100%;height:320px;min-height:320px;")}
      >
        <canvas ref={canvasRef} style={css("display:block;")} />
        {loading && drawCandles.length < 2 && (
          <div
            style={css(
              "position:absolute;inset:0;display:flex;align-items:center;justify-content:center;color:#414141;font-size:12.5px;font-weight:600;",
            )}
          >
            Loading market…
          </div>
        )}
        {!loading && drawCandles.length < 2 && (
          <div
            style={css(
              "position:absolute;inset:0;display:flex;align-items:center;justify-content:center;color:#414141;font-size:12.5px;font-weight:600;",
            )}
          >
            No chart data
          </div>
        )}
      </div>
    </section>
  );
}
