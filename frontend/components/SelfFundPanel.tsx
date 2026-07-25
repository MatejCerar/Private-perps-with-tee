"use client";

import { useState } from "react";
import { css, C, usd, truncAddr } from "@/lib/ui";
import { hlExplorerAddress } from "@/lib/config";

// Shown when a non-sponsored deposit returns selfFund:true. The user must fund
// the derived HL address via the HL testnet faucet, then confirm so the TEE can
// verify the balance and credit the ledger.
export function SelfFundPanel(props: {
  address: string;
  amountUsd: number;
  busy: boolean;
  onConfirm: () => void;
  onDismiss: () => void;
}) {
  const [copied, setCopied] = useState(false);

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(props.address);
      setCopied(true);
      setTimeout(() => setCopied(false), 1600);
    } catch {
      /* clipboard unavailable */
    }
  };

  return (
    <div
      style={css(
        "border-radius:18px;border:1px solid rgba(227,173,70,0.35);background:linear-gradient(180deg,rgba(227,173,70,0.07),rgba(227,173,70,0.02));padding:18px;margin-bottom:22px;animation:hypeRise 0.28s ease;",
      )}
    >
      <div style={css("display:flex;align-items:center;gap:10px;margin-bottom:14px;")}>
        <span
          style={css(
            "width:30px;height:30px;border-radius:9px;background:rgba(227,173,70,0.16);color:#e8c885;display:flex;align-items:center;justify-content:center;font-size:15px;",
          )}
        >
          ⛽
        </span>
        <div style={css("flex:1;min-width:0;")}>
          <div style={css("font-size:14px;font-weight:800;color:#e9ecf4;")}>
            Fund your Hyperliquid account
          </div>
          <div style={css("font-size:12px;color:#8a93a6;")}>
            You're bringing your own testnet USDC — send {usd(props.amountUsd)} to the address
            below, then confirm.
          </div>
        </div>
        <button
          onClick={props.onDismiss}
          style={css(
            "width:26px;height:26px;border-radius:8px;border:1px solid rgba(255,255,255,0.1);background:transparent;color:#8a93a6;font-size:13px;cursor:pointer;flex-shrink:0;",
          )}
        >
          ✕
        </button>
      </div>

      {/* address + copy */}
      <div
        style={css(
          "display:flex;align-items:center;gap:10px;padding:12px 14px;border-radius:12px;background:rgba(0,0,0,0.3);border:1px solid rgba(255,255,255,0.07);margin-bottom:14px;",
        )}
      >
        <span
          style={css(
            "flex:1;font-family:var(--font-jetbrains),ui-monospace,monospace;font-size:12.5px;color:#8aa0ff;word-break:break-all;line-height:1.5;",
          )}
        >
          {props.address}
        </span>
        <button
          onClick={copy}
          style={css(
            "flex-shrink:0;padding:7px 12px;border-radius:9px;border:1px solid rgba(255,255,255,0.12);background:rgba(255,255,255,0.04);color:#c8cede;font-size:11.5px;font-weight:700;cursor:pointer;transition:background .15s;",
          )}
        >
          {copied ? "Copied ✓" : "Copy"}
        </button>
      </div>

      {/* steps */}
      <div style={css("display:flex;flex-direction:column;gap:8px;margin-bottom:16px;")}>
        <Step n={1} label="Open the HL testnet faucet" />
        <Step n={2} label={`Send ${usd(props.amountUsd)} USDC to ${truncAddr(props.address)}`} />
        <Step n={3} label="Come back and confirm below" />
      </div>

      <div style={css("display:grid;grid-template-columns:1fr 1fr;gap:9px;")}>
        <a
          href="https://app.hyperliquid-testnet.xyz/"
          target="_blank"
          rel="noreferrer"
          style={css(
            "display:flex;align-items:center;justify-content:center;padding:11px;border-radius:11px;border:1px solid rgba(255,255,255,0.12);background:rgba(255,255,255,0.03);color:#e9ecf4;font-size:13.5px;font-weight:700;text-decoration:none;",
          )}
        >
          HL faucet ↗
        </a>
        <button
          onClick={props.onConfirm}
          disabled={props.busy}
          style={css(
            `padding:11px;border-radius:11px;border:none;background:${C.accent};color:#0b0d13;font-size:13.5px;font-weight:800;cursor:pointer;` +
              (props.busy ? "opacity:0.5;cursor:not-allowed;" : ""),
          )}
        >
          {props.busy ? "Confirming…" : "I've funded it — confirm"}
        </button>
      </div>

      <div style={css("margin-top:12px;text-align:center;")}>
        <a
          href={hlExplorerAddress(props.address)}
          target="_blank"
          rel="noreferrer"
          style={css("font-size:11px;font-weight:700;color:#8a93a6;text-decoration:none;")}
        >
          View account on HL explorer ↗
        </a>
      </div>
    </div>
  );
}

function Step({ n, label }: { n: number; label: string }) {
  return (
    <div style={css("display:flex;align-items:center;gap:10px;")}>
      <span
        style={css(
          "flex-shrink:0;width:20px;height:20px;border-radius:50%;background:rgba(227,173,70,0.16);color:#e8c885;display:flex;align-items:center;justify-content:center;font-size:11px;font-weight:800;",
        )}
      >
        {n}
      </span>
      <span style={css("font-size:12.5px;color:#c8cede;font-weight:600;")}>{label}</span>
    </div>
  );
}
