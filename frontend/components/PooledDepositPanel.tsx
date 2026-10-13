"use client";

import { useEffect, useState } from "react";
import { css, C, usd } from "@/lib/ui";
import { hlExplorerAddress } from "@/lib/config";

// Shown after a deposit returns pendingFunding:true. The deposit has been
// escrowed and the HL account will be funded privately after a randomized delay,
// batched with other deposits so the Flare<->HL link stays private. We show a
// calm explanation and a live countdown, never any batch internals.
function fmtCountdown(seconds: number): string {
  const s = Math.max(0, Math.round(seconds));
  if (s >= 90) return `~${Math.round(s / 60)} min`;
  if (s > 0) return `~${s} sec`;
  return "finalizing…";
}

function shortAddr(a?: string): string {
  return a && a.length > 10 ? `${a.slice(0, 6)}…${a.slice(-4)}` : a ?? "";
}

export function PooledDepositPanel(props: {
  address?: string;
  amountUsd: number;
  estimatedSeconds: number;
  busy: boolean;
  onCheckStatus: () => void;
  onDismiss: () => void;
}) {
  const [remaining, setRemaining] = useState(props.estimatedSeconds);

  useEffect(() => {
    setRemaining(props.estimatedSeconds);
  }, [props.estimatedSeconds]);

  useEffect(() => {
    const t = setInterval(() => {
      setRemaining((r) => (r > 0 ? r - 1 : 0));
    }, 1000);
    return () => clearInterval(t);
  }, []);

  return (
    <div
      style={css(
        "border-radius:18px;border:1px solid rgba(230,32,88,0.35);background:linear-gradient(180deg,rgba(230,32,88,0.08),rgba(230,32,88,0.02));padding:18px;margin-bottom:22px;animation:hypeRise 0.28s ease;",
      )}
    >
      <div style={css("display:flex;align-items:center;gap:10px;margin-bottom:14px;")}>
        <span
          style={css(
            "width:30px;height:30px;border-radius:9px;background:rgba(230,32,88,0.16);color:#fd6f8c;display:flex;align-items:center;justify-content:center;font-size:14px;font-weight:800;",
          )}
        >
          ○
        </span>
        <div style={css("flex:1;min-width:0;")}>
          <div style={css("font-size:14px;font-weight:800;color:#f6f6f6;")}>
            Deposit received, funding privately
          </div>
          <div style={css("font-size:12px;color:#777777;")}>
            Your {usd(props.amountUsd)} is escrowed and will reach your Hyperliquid account on a
            short delay.
          </div>
        </div>
        <button
          onClick={props.onDismiss}
          style={css(
            "width:26px;height:26px;border-radius:8px;border:1px solid rgba(255,255,255,0.1);background:transparent;color:#777777;font-size:13px;cursor:pointer;flex-shrink:0;",
          )}
        >
          ✕
        </button>
      </div>

      {/* countdown */}
      <div
        style={css(
          "display:flex;align-items:center;justify-content:space-between;gap:10px;padding:14px 15px;border-radius:12px;background:rgba(0,0,0,0.3);border:1px solid rgba(255,255,255,0.07);margin-bottom:14px;",
        )}
      >
        <div style={css("display:flex;flex-direction:column;gap:2px;")}>
          <span
            style={css(
              "font-size:11px;color:#777777;font-weight:600;letter-spacing:0.04em;text-transform:uppercase;",
            )}
          >
            Funded privately in
          </span>
          <span
            style={css(
              "font-size:22px;font-weight:800;color:#f6f6f6;font-variant-numeric:tabular-nums;",
            )}
          >
            {fmtCountdown(remaining)}
          </span>
        </div>
        <span
          style={css(
            "width:14px;height:14px;border-radius:50%;background:#e62058;animation:hypePulse 2s ease-in-out infinite;flex-shrink:0;",
          )}
        />
      </div>

      {/* calm explanation */}
      <div style={css("font-size:12.5px;color:#d1d1d1;line-height:1.55;margin-bottom:16px;")}>
        To keep your Flare wallet and Hyperliquid account unlinkable, deposits are pooled and
        funded together after a randomized delay. Your balance stays at $0.00 until funding lands,
        then it updates automatically.
      </div>

      {props.address && (
        <div
          style={css(
            "display:flex;align-items:center;justify-content:space-between;gap:8px;padding:9px 11px;border-radius:10px;background:rgba(0,0,0,0.25);border:1px solid rgba(255,255,255,0.06);margin-bottom:12px;",
          )}
        >
          <span style={css("font-size:11px;color:#777777;font-weight:600;")}>Your HL account</span>
          <span style={css("font-size:12.5px;color:#d1d1d1;font-family:monospace;")}>
            {shortAddr(props.address)}
          </span>
        </div>
      )}

      <div style={css("display:grid;grid-template-columns:1fr 1fr;gap:9px;")}>
        <a
          href={props.address ? hlExplorerAddress(props.address) : "#"}
          target="_blank"
          rel="noreferrer"
          style={css(
            "display:flex;align-items:center;justify-content:center;padding:11px;border-radius:11px;border:1px solid rgba(255,255,255,0.12);background:rgba(255,255,255,0.03);color:#f6f6f6;font-size:13.5px;font-weight:700;text-decoration:none;" +
              (props.address ? "" : "opacity:0.5;pointer-events:none;"),
          )}
        >
          View HL account ↗
        </a>
        <button
          onClick={props.onCheckStatus}
          disabled={props.busy}
          style={css(
            `padding:11px;border-radius:11px;border:none;background:${C.accent};color:#ffffff;font-size:13.5px;font-weight:800;cursor:pointer;` +
              (props.busy ? "opacity:0.5;cursor:not-allowed;" : ""),
          )}
        >
          Check status
        </button>
      </div>
    </div>
  );
}
