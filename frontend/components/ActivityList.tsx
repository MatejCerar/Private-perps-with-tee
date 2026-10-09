"use client";

import { css } from "@/lib/ui";
import { explorerTx } from "@/lib/config";

export type ActivityEntry = {
  kind: "deposit" | "withdraw";
  hash: `0x${string}`;
  ts: number;
};

// Persistent "Recent on-chain activity" list for the right column (under the
// BalanceCard). Shows both directions of the on-chain flow - deposit escrow and
// withdraw release - so the user can always jump to the Flare explorer, even
// after the StatusToast disappears.
function relTime(ts: number): string {
  const s = Math.max(0, Math.round((Date.now() - ts) / 1000));
  if (s < 60) return `${s}s ago`;
  const m = Math.round(s / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.round(m / 60);
  if (h < 24) return `${h}h ago`;
  return `${Math.round(h / 24)}d ago`;
}

function truncHash(hash: string): string {
  return `${hash.slice(0, 10)}...${hash.slice(-6)}`;
}

export function ActivityList(props: { entries: ActivityEntry[] }) {
  return (
    <div
      style={css(
        "border-radius:16px;border:1px solid rgba(255,255,255,0.08);background:#1a1a1a;padding:18px;margin-top:16px;",
      )}
    >
      <div style={css("margin-bottom:12px;")}>
        <span
          style={css(
            "font-size:11px;color:#777777;font-weight:700;letter-spacing:0.08em;text-transform:uppercase;",
          )}
        >
          Recent on-chain activity
        </span>
      </div>

      {props.entries.length === 0 ? (
        <div style={css("font-size:12.5px;color:#414141;font-weight:600;")}>
          No on-chain activity yet
        </div>
      ) : (
        <div style={css("display:flex;flex-direction:column;gap:8px;")}>
          {props.entries.map((e) => {
            const isDeposit = e.kind === "deposit";
            const label = isDeposit
              ? "Deposit -> escrow (Vault)"
              : "Withdraw -> release (Vault)";
            const dot = isDeposit ? "#61b854" : "#e62058";
            return (
              <a
                key={e.hash}
                href={explorerTx(e.hash)}
                target="_blank"
                rel="noreferrer"
                style={css(
                  "display:flex;align-items:center;gap:10px;padding:10px 12px;border-radius:11px;background:rgba(255,255,255,0.03);border:1px solid rgba(255,255,255,0.06);text-decoration:none;",
                )}
              >
                <span
                  style={css(
                    `width:7px;height:7px;border-radius:50%;flex-shrink:0;background:${dot};`,
                  )}
                />
                <div style={css("flex:1;min-width:0;")}>
                  <div
                    style={css(
                      "font-size:12.5px;font-weight:700;color:#f6f6f6;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;",
                    )}
                  >
                    {label}
                  </div>
                  <div
                    style={css(
                      "font-size:10.5px;color:#777777;font-variant-numeric:tabular-nums;font-family:var(--font-jetbrains),ui-monospace,monospace;",
                    )}
                  >
                    {truncHash(e.hash)} · {relTime(e.ts)}
                  </div>
                </div>
                <span
                  style={css("font-size:11px;font-weight:700;color:#f73c68;flex-shrink:0;")}
                >
                  explorer ↗
                </span>
              </a>
            );
          })}
        </div>
      )}
    </div>
  );
}
