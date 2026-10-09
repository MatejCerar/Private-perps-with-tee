"use client";

import { css, C, usd } from "@/lib/ui";
import { hlExplorerAddress } from "@/lib/config";

// The "Your money" card for the right column: total equity, available / in
// positions split, and the Deposit / Withdraw triggers. Mirrors the original
// AccountColumn money card; it just sits under the TradeCard now.
export function BalanceCard(props: {
  balance: number;
  available: number;
  inPositions: number;
  everDeposited: boolean;
  hlAddress?: string;
  onDeposit: () => void;
  onWithdraw: () => void;
}) {
  return (
    <div
      style={css(
        "border-radius:16px;border:1px solid rgba(255,255,255,0.08);background:#1a1a1a;padding:18px;",
      )}
    >
      <div style={css("display:flex;align-items:center;justify-content:space-between;margin-bottom:12px;")}>
        <span
          style={css(
            "font-size:11px;color:#777777;font-weight:700;letter-spacing:0.08em;text-transform:uppercase;",
          )}
        >
          Your money
        </span>
        <span style={css("display:flex;align-items:center;gap:10px;")}>
          {props.hlAddress && (
            <a
              href={hlExplorerAddress(props.hlAddress)}
              target="_blank"
              rel="noreferrer"
              style={css("font-size:10.5px;font-weight:700;color:#f73c68;text-decoration:none;")}
            >
              View on HL ↗
            </a>
          )}
          <span style={css("display:flex;align-items:center;gap:5px;font-size:10.5px;color:#414141;font-weight:600;")}>
            <span
              style={css(
                "width:5px;height:5px;border-radius:50%;background:#61b854;animation:hypePulse 2s ease-in-out infinite;",
              )}
            />
            Live
          </span>
        </span>
      </div>
      <div
        style={css(
          "font-size:32px;font-weight:800;letter-spacing:-0.02em;font-variant-numeric:tabular-nums;margin-bottom:3px;",
        )}
      >
        {usd(props.balance)}
      </div>
      <div style={css("font-size:12px;color:#777777;margin-bottom:14px;")}>
        {props.everDeposited ? "Total equity" : "Deposit to start"}
      </div>
      {props.everDeposited && (
        <div
          style={css(
            "display:flex;gap:16px;padding:10px 13px;border-radius:11px;background:rgba(255,255,255,0.03);border:1px solid rgba(255,255,255,0.06);margin-bottom:16px;",
          )}
        >
          <div style={css("flex:1;")}>
            <div style={css("font-size:10px;color:#777777;font-weight:600;letter-spacing:0.04em;text-transform:uppercase;margin-bottom:3px;")}>
              Available
            </div>
            <div style={css("font-size:14px;font-weight:700;font-variant-numeric:tabular-nums;color:#f6f6f6;")}>
              {usd(props.available)}
            </div>
          </div>
          <div style={css("width:1px;background:rgba(255,255,255,0.08);")} />
          <div style={css("flex:1;")}>
            <div style={css("font-size:10px;color:#777777;font-weight:600;letter-spacing:0.04em;text-transform:uppercase;margin-bottom:3px;")}>
              In positions
            </div>
            <div style={css("font-size:14px;font-weight:700;font-variant-numeric:tabular-nums;color:#f6f6f6;")}>
              {usd(props.inPositions)}
            </div>
          </div>
        </div>
      )}
      <div style={css("display:grid;grid-template-columns:1fr 1fr;gap:8px;")}>
        <button
          onClick={props.onDeposit}
          style={css(
            `padding:11px;border-radius:11px;border:none;background:${C.accent};color:#ffffff;font-size:13.5px;font-weight:800;cursor:pointer;`,
          )}
        >
          Deposit
        </button>
        <button
          onClick={props.onWithdraw}
          style={css(
            "padding:11px;border-radius:11px;border:1px solid rgba(255,255,255,0.12);background:rgba(255,255,255,0.03);color:#f6f6f6;font-size:13.5px;font-weight:700;cursor:pointer;",
          )}
        >
          Withdraw
        </button>
      </div>
    </div>
  );
}
