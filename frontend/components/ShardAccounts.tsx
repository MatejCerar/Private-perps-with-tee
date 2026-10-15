"use client";

import { useEffect, useState } from "react";
import { css, usd } from "@/lib/ui";
import { hlExplorerAddress } from "@/lib/config";
import { SPLIT_MODE, RELAYER_ENABLED, fetchShards, type ShardAccount } from "@/lib/relay";

function shortAddr(a: string): string {
  return a && a.length > 10 ? `${a.slice(0, 6)}…${a.slice(-4)}` : a;
}

// Split mode only: lists the separate Hyperliquid accounts a deposit fanned out
// across, each with its balance and its own "View on HL" link. This is what makes
// the privacy split visible - you can open each account on the explorer and see it
// holds only its slice, with no on-chain link to your wallet or the others.
export function ShardAccounts({ user }: { user?: string }) {
  const [shards, setShards] = useState<ShardAccount[]>([]);
  const [open, setOpen] = useState(true);

  useEffect(() => {
    if (!(SPLIT_MODE && RELAYER_ENABLED && user)) return;
    let alive = true;
    const load = async () => {
      try {
        const s = await fetchShards(user);
        if (alive) setShards(s);
      } catch {
        /* transient - keep last */
      }
    };
    load();
    const t = setInterval(load, 6000);
    return () => {
      alive = false;
      clearInterval(t);
    };
  }, [user]);

  if (!(SPLIT_MODE && RELAYER_ENABLED)) return null;
  const funded = shards.filter((s) => s.balanceUsd > 0.01);
  if (funded.length === 0) return null;

  return (
    <div
      style={css(
        "border-radius:16px;border:1px solid rgba(255,255,255,0.08);background:#1a1a1a;padding:16px;",
      )}
    >
      <button
        onClick={() => setOpen(!open)}
        style={css(
          "width:100%;display:flex;align-items:center;justify-content:space-between;background:transparent;border:none;cursor:pointer;padding:0;color:inherit;",
        )}
      >
        <span
          style={css(
            "font-size:11px;color:#777777;font-weight:700;letter-spacing:0.08em;text-transform:uppercase;",
          )}
        >
          Private accounts ({funded.length})
        </span>
        <span style={css("font-size:12px;color:#777777;")}>{open ? "▾" : "▸"}</span>
      </button>

      {open && (
        <div style={css("margin-top:12px;display:flex;flex-direction:column;gap:7px;")}>
          {funded.map((s) => (
            <div
              key={s.shard}
              style={css(
                "display:flex;align-items:center;justify-content:space-between;gap:8px;padding:9px 11px;border-radius:10px;background:rgba(0,0,0,0.25);border:1px solid rgba(255,255,255,0.06);",
              )}
            >
              <span style={css("font-size:12.5px;color:#d1d1d1;font-family:monospace;")}>
                {shortAddr(s.hlAddress)}
              </span>
              <span
                style={css(
                  "font-size:12.5px;font-weight:700;color:#f6f6f6;font-variant-numeric:tabular-nums;",
                )}
              >
                {usd(s.balanceUsd)}
              </span>
              <a
                href={hlExplorerAddress(s.hlAddress)}
                target="_blank"
                rel="noreferrer"
                style={css(
                  "font-size:10.5px;font-weight:700;color:#f73c68;text-decoration:none;white-space:nowrap;",
                )}
              >
                View on HL ↗
              </a>
            </div>
          ))}
          <div style={css("font-size:11px;color:#777777;line-height:1.5;margin-top:4px;")}>
            Each deposit is split across these separate Hyperliquid accounts, so no single
            account reveals your total or links back to your wallet.
          </div>
        </div>
      )}
    </div>
  );
}
