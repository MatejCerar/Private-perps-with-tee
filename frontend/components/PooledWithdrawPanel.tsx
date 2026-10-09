"use client";

import { useEffect, useState } from "react";
import { css, usd } from "@/lib/ui";

// Shown after a withdraw returns pendingPayout:true. The mirror of the deposit
// panel: the balance has already been debited, and the HL account is unwound to
// the treasury privately after a randomized, batched delay so the exit cannot be
// linked back to the Flare wallet. Calm explanation + a live countdown.
function fmtCountdown(seconds: number): string {
  const s = Math.max(0, Math.round(seconds));
  if (s >= 90) return `~${Math.round(s / 60)} min`;
  if (s > 0) return `~${s} sec`;
  return "any moment";
}

export function PooledWithdrawPanel(props: {
  amountUsd: number;
  estimatedSeconds: number;
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
            Withdrawal is being settled privately
          </div>
          <div style={css("font-size:12px;color:#777777;")}>
            Your {usd(props.amountUsd)} was debited now and leaves your Hyperliquid account on a
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
            Paid out privately in
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
      <div style={css("font-size:12.5px;color:#d1d1d1;line-height:1.55;")}>
        Your balance was debited immediately. To keep your Flare wallet and Hyperliquid account
        unlinkable, the account is unwound to the treasury after a randomized, batched delay, so
        the exit cannot be tied back to you. No further action is needed.
      </div>
    </div>
  );
}
