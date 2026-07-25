"use client";

import { css, C, num, usd } from "@/lib/ui";

export function TxModal(props: {
  kind: "deposit" | "withdraw";
  amount: string;
  balance: number;
  withdrawable: number;
  maxDeposit: number;
  busy: boolean;
  onAmount: (v: string) => void;
  onMax: () => void;
  onPick: (v: string) => void;
  onClose: () => void;
  onSubmit: () => void;
}) {
  const isDep = props.kind === "deposit";
  const amt = num(props.amount);
  const room = Math.max(0, props.maxDeposit - props.balance);

  let errorText = "";
  if (isDep && props.balance + amt > props.maxDeposit)
    errorText = `Exceeds the $${props.maxDeposit} per-wallet testnet cap.`;
  if (!isDep && amt > props.withdrawable) errorText = "More than your withdrawable balance.";
  const valid = amt > 0 && !errorText && !props.busy;

  const btnStyle =
    "width:100%;padding:14px;border-radius:13px;border:none;font-size:15px;font-weight:800;" +
    (valid
      ? `background:${C.accent};color:#0b0d13;cursor:pointer;`
      : "background:rgba(255,255,255,0.05);color:#5f6779;cursor:not-allowed;");

  return (
    <div
      onClick={props.onClose}
      style={css(
        "position:fixed;inset:0;z-index:60;display:flex;align-items:center;justify-content:center;padding:24px;background:rgba(4,6,10,0.66);backdrop-filter:blur(6px);animation:hypeRise 0.18s ease;",
      )}
    >
      <div
        onClick={(e) => e.stopPropagation()}
        style={css(
          "width:100%;max-width:420px;border-radius:20px;border:1px solid rgba(255,255,255,0.1);background:linear-gradient(180deg,#141924,#0f131b);box-shadow:0 40px 90px -30px rgba(0,0,0,0.9);padding:22px;",
        )}
      >
        <div style={css("display:flex;align-items:center;justify-content:space-between;margin-bottom:6px;")}>
          <span style={css("font-size:18px;font-weight:800;")}>
            {isDep ? "Deposit test dollars" : "Withdraw"}
          </span>
          <button
            onClick={props.onClose}
            style={css(
              "width:30px;height:30px;border-radius:9px;border:1px solid rgba(255,255,255,0.1);background:transparent;color:#8a93a6;font-size:15px;cursor:pointer;",
            )}
          >
            ✕
          </button>
        </div>
        <div style={css("font-size:12.5px;color:#8a93a6;line-height:1.5;margin-bottom:18px;")}>
          {isDep
            ? "Mints test USDC to your wallet, escrows it on Coston, then the treasury funds your Hyperliquid account. A couple of wallet signatures; ~15s. No real value."
            : "Unwinds on Hyperliquid, then releases your test USDC back to your Coston wallet (request + release signatures)."}
        </div>

        <div
          style={css(
            "border-radius:14px;border:1px solid rgba(255,255,255,0.08);background:rgba(0,0,0,0.22);padding:14px 15px;margin-bottom:12px;",
          )}
        >
          <div style={css("display:flex;justify-content:space-between;align-items:center;margin-bottom:6px;")}>
            <span style={css("font-size:12px;color:#8a93a6;font-weight:600;")}>Amount</span>
            <span style={css("font-size:11.5px;color:#6f7789;")}>
              {isDep ? `Up to $${room.toFixed(0)} more` : `Withdrawable ${usd(props.withdrawable)}`}
            </span>
          </div>
          <div style={css("display:flex;align-items:center;gap:10px;")}>
            <span style={css("font-size:26px;font-weight:800;color:#8a93a6;")}>$</span>
            <input
              value={props.amount}
              onChange={(e) => props.onAmount(e.target.value.replace(/[^0-9.]/g, ""))}
              inputMode="decimal"
              placeholder="0"
              autoFocus
              style={css(
                "flex:1;min-width:0;border:none;background:transparent;color:#e9ecf4;font-size:30px;font-weight:800;font-variant-numeric:tabular-nums;padding:0;",
              )}
            />
            <button
              onClick={props.onMax}
              style={css(
                "padding:5px 11px;border-radius:8px;border:1px solid rgba(124,140,255,0.3);background:rgba(124,140,255,0.1);color:#a7b6ff;font-size:11.5px;font-weight:800;cursor:pointer;",
              )}
            >
              MAX
            </button>
          </div>
        </div>

        {isDep && (
          <div style={css("display:grid;grid-template-columns:repeat(4,1fr);gap:8px;margin-bottom:14px;")}>
            {["25", "50", "100", "Max"].map((v) => (
              <button
                key={v}
                onClick={() => (v === "Max" ? props.onMax() : props.onPick(v))}
                style={css(
                  "padding:9px;border-radius:10px;font-size:12.5px;font-weight:800;cursor:pointer;border:1px solid rgba(255,255,255,0.09);background:rgba(255,255,255,0.03);color:#c8cede;",
                )}
              >
                {v === "Max" ? "Max" : `$${v}`}
              </button>
            ))}
          </div>
        )}

        {errorText && (
          <div style={css("font-size:12px;color:#ff8082;font-weight:600;margin-bottom:12px;")}>
            {errorText}
          </div>
        )}

        <button onClick={props.onSubmit} style={css(btnStyle)}>
          {props.busy ? "One action at a time…" : isDep ? "Deposit" : "Withdraw"}
        </button>
      </div>
    </div>
  );
}
