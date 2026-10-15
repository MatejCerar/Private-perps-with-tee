"use client";

import { css, C, num, usd } from "@/lib/ui";

export function TxModal(props: {
  kind: "deposit" | "withdraw";
  amount: string;
  balance: number;
  withdrawable: number;
  maxDeposit: number;
  busy: boolean;
  privacy: boolean;
  split?: boolean;
  denominations: number[];
  onAmount: (v: string) => void;
  onMax: () => void;
  onPick: (v: string) => void;
  onClose: () => void;
  onSubmit: () => void;
}) {
  const isDep = props.kind === "deposit";
  const amt = num(props.amount);
  const room = Math.max(0, props.maxDeposit - props.balance);
  // Non-split privacy mode locks deposits to fixed denominations (read-only input).
  // Split mode accepts any amount >= the smallest denomination, so the input is
  // free and the denominations become quick-fill chips.
  const minDenom = props.denominations.length ? Math.min(...props.denominations) : 0;
  const fixedDenoms = isDep && props.privacy && !props.split;
  const depositChips = fixedDenoms
    ? props.denominations.map((d) => String(d))
    : props.split
      ? [...props.denominations.map((d) => String(d)), "Max"]
      : ["25", "50", "100", "Max"];

  let errorText = "";
  if (isDep && props.balance + amt > props.maxDeposit)
    errorText = `Exceeds the $${props.maxDeposit} per-wallet testnet cap.`;
  if (isDep && props.split && amt > 0 && amt < minDenom)
    errorText = `Minimum deposit is $${minDenom}.`;
  if (!isDep && amt > props.withdrawable) errorText = "More than your withdrawable balance.";
  const valid = amt > 0 && !errorText && !props.busy;

  const btnStyle =
    "width:100%;padding:14px;border-radius:13px;border:none;font-size:15px;font-weight:800;" +
    (valid
      ? `background:${C.accent};color:#ffffff;cursor:pointer;`
      : "background:rgba(255,255,255,0.05);color:#414141;cursor:not-allowed;");

  return (
    <div
      onClick={props.onClose}
      style={css(
        "position:fixed;inset:0;z-index:60;display:flex;align-items:center;justify-content:center;padding:24px;background:rgba(16,16,16,0.66);backdrop-filter:blur(6px);animation:hypeRise 0.18s ease;",
      )}
    >
      <div
        onClick={(e) => e.stopPropagation()}
        style={css(
          "width:100%;max-width:420px;border-radius:20px;border:1px solid rgba(255,255,255,0.1);background:linear-gradient(180deg,#232323,#1a1a1a);box-shadow:0 40px 90px -30px rgba(0,0,0,0.9);padding:22px;",
        )}
      >
        <div style={css("display:flex;align-items:center;justify-content:space-between;margin-bottom:6px;")}>
          <span style={css("font-size:18px;font-weight:800;")}>
            {isDep ? "Deposit test dollars" : "Withdraw"}
          </span>
          <button
            onClick={props.onClose}
            style={css(
              "width:30px;height:30px;border-radius:9px;border:1px solid rgba(255,255,255,0.1);background:transparent;color:#777777;font-size:15px;cursor:pointer;",
            )}
          >
            ✕
          </button>
        </div>
        <div style={css("font-size:12.5px;color:#777777;line-height:1.5;margin-bottom:18px;")}>
          {!isDep
            ? "Unwinds on Hyperliquid, then releases your test USDC back to your Coston wallet (request + release signatures)."
            : fixedDenoms
              ? "Mints test USDC to your wallet and escrows it on Coston. Your Hyperliquid account is then funded privately, batched with others on a short delay. No real value."
              : props.split
                ? "Mints test USDC and escrows it on Coston. Your deposit is then split across several separate Hyperliquid accounts and funded on a short, randomized delay. Any amount $10+; any sub-unit remainder is kept as credit. No real value."
                : "Mints test USDC to your wallet, escrows it on Coston, then the treasury funds your Hyperliquid account. A couple of wallet signatures; ~15s. No real value."}
        </div>

        <div
          style={css(
            "border-radius:14px;border:1px solid rgba(255,255,255,0.08);background:rgba(0,0,0,0.22);padding:14px 15px;margin-bottom:12px;",
          )}
        >
          <div style={css("display:flex;justify-content:space-between;align-items:center;margin-bottom:6px;")}>
            <span style={css("font-size:12px;color:#777777;font-weight:600;")}>Amount</span>
            <span style={css("font-size:11.5px;color:#414141;")}>
              {isDep ? `Up to $${room.toFixed(0)} more` : `Withdrawable ${usd(props.withdrawable)}`}
            </span>
          </div>
          <div style={css("display:flex;align-items:center;gap:10px;")}>
            <span style={css("font-size:26px;font-weight:800;color:#777777;")}>$</span>
            <input
              value={props.amount}
              onChange={(e) => props.onAmount(e.target.value.replace(/[^0-9.]/g, ""))}
              inputMode="decimal"
              placeholder="0"
              autoFocus={!fixedDenoms}
              readOnly={fixedDenoms}
              style={css(
                "flex:1;min-width:0;border:none;background:transparent;color:#f6f6f6;font-size:30px;font-weight:800;font-variant-numeric:tabular-nums;padding:0;" +
                  (fixedDenoms ? "cursor:default;" : ""),
              )}
            />
            {!fixedDenoms && (
              <button
                onClick={props.onMax}
                style={css(
                  "padding:5px 11px;border-radius:8px;border:1px solid rgba(230,32,88,0.3);background:rgba(230,32,88,0.1);color:#fd6f8c;font-size:11.5px;font-weight:800;cursor:pointer;",
                )}
              >
                MAX
              </button>
            )}
          </div>
        </div>

        {isDep && (
          <div
            style={css(
              `display:grid;grid-template-columns:repeat(${depositChips.length},1fr);gap:8px;margin-bottom:${fixedDenoms ? "10" : "14"}px;`,
            )}
          >
            {depositChips.map((v) => {
              const selected = fixedDenoms && props.amount === v;
              return (
                <button
                  key={v}
                  onClick={() => (v === "Max" ? props.onMax() : props.onPick(v))}
                  style={css(
                    "padding:9px;border-radius:10px;font-size:12.5px;font-weight:800;cursor:pointer;" +
                      (selected
                        ? `border:1px solid ${C.accent};background:rgba(230,32,88,0.14);color:#ffccd5;`
                        : "border:1px solid rgba(255,255,255,0.09);background:rgba(255,255,255,0.03);color:#d1d1d1;"),
                  )}
                >
                  {v === "Max" ? "Max" : `$${v}`}
                </button>
              );
            })}
          </div>
        )}

        {fixedDenoms && (
          <div
            style={css(
              "font-size:11.5px;color:#777777;line-height:1.5;margin-bottom:14px;",
            )}
          >
            Private deposits use fixed amounts so your deposit blends into the batch. Pick one
            above.
          </div>
        )}

        {isDep && props.split && (
          <div
            style={css(
              "font-size:11.5px;color:#777777;line-height:1.5;margin-bottom:14px;",
            )}
          >
            Type any amount (min ${minDenom}), or tap a quick amount. Your deposit is split across
            several separate private accounts; the sub-unit remainder is kept as credit.
          </div>
        )}

        {errorText && (
          <div style={css("font-size:12px;color:#f2726c;font-weight:600;margin-bottom:12px;")}>
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
