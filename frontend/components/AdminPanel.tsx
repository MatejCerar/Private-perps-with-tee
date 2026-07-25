"use client";

import { useCallback, useEffect, useState } from "react";
import { usePublicClient, useWriteContract } from "wagmi";
import { instructionSenderAbi } from "@/lib/abi";
import { activeChain, INSTRUCTION_SENDER, unitsToUsd, usdToUnits } from "@/lib/config";
import { css, C, usd, truncAddr } from "@/lib/ui";

// Admin panel for managing the sponsored funding allowlist. Visible to the
// contract owner and any address with isAdmin=true on-chain. Admin management
// (add/remove admins) is owner-only. The panel is self-contained: it checks the
// caller's role on-chain and renders nothing for non-admins. Closing it collapses
// to a small chip that only admins can see.
export function AdminPanel(props: { address: `0x${string}` }) {
  const publicClient = usePublicClient();
  const { writeContractAsync } = useWriteContract();

  const [role, setRole] = useState<"owner" | "admin" | null>(null);
  const [checking, setChecking] = useState(true);
  const [open, setOpen] = useState(true);

  // Form state
  const [targetAddr, setTargetAddr] = useState("");
  const [limitUsd, setLimitUsd] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | undefined>();
  const [success, setSuccess] = useState<string | undefined>();

  // Lookup state
  const [lookupAddr, setLookupAddr] = useState("");
  const [lookupLimit, setLookupLimit] = useState<number | null>(null);

  // Admin management state (owner-only)
  const [adminAddr, setAdminAddr] = useState("");
  const [adminBusy, setAdminBusy] = useState(false);

  // Check the connected wallet's role: owner, admin, or neither.
  useEffect(() => {
    if (!publicClient) return;
    setChecking(true);
    (async () => {
      try {
        const [owner, admin] = await Promise.all([
          publicClient.readContract({
            address: INSTRUCTION_SENDER,
            abi: instructionSenderAbi,
            functionName: "owner",
          }) as Promise<string>,
          publicClient.readContract({
            address: INSTRUCTION_SENDER,
            abi: instructionSenderAbi,
            functionName: "isAdmin",
            args: [props.address],
          }) as Promise<boolean>,
        ]);
        if (owner.toLowerCase() === props.address.toLowerCase()) setRole("owner");
        else if (admin) setRole("admin");
        else setRole(null);
      } catch {
        setRole(null);
      } finally {
        setChecking(false);
      }
    })();
  }, [publicClient, props.address]);

  const setLimit = useCallback(async () => {
    if (!publicClient || busy) return;
    setError(undefined);
    setSuccess(undefined);
    const addr = targetAddr.trim();
    if (!/^0x[0-9a-fA-F]{40}$/.test(addr)) {
      setError("Enter a valid 0x address");
      return;
    }
    const amt = parseFloat(limitUsd);
    if (!Number.isFinite(amt) || amt < 0) {
      setError("Enter a valid USD amount (0 to revoke)");
      return;
    }
    setBusy(true);
    try {
      const units = usdToUnits(amt);
      const hash = await writeContractAsync({
        address: INSTRUCTION_SENDER,
        abi: instructionSenderAbi,
        functionName: "setSponsoredLimit",
        args: [addr as `0x${string}`, units],
        chainId: activeChain.id,
      });
      await publicClient.waitForTransactionReceipt({ hash });
      setSuccess(
        amt === 0
          ? `Revoked sponsorship for ${truncAddr(addr)}`
          : `Set ${truncAddr(addr)} limit to ${usd(amt)}`,
      );
      setTargetAddr("");
      setLimitUsd("");
    } catch (e) {
      setError(e instanceof Error ? e.message.split("\n")[0].slice(0, 200) : String(e));
    } finally {
      setBusy(false);
    }
  }, [publicClient, busy, targetAddr, limitUsd, writeContractAsync]);

  const lookup = useCallback(async () => {
    if (!publicClient) return;
    const addr = lookupAddr.trim();
    if (!/^0x[0-9a-fA-F]{40}$/.test(addr)) return;
    try {
      const limit = (await publicClient.readContract({
        address: INSTRUCTION_SENDER,
        abi: instructionSenderAbi,
        functionName: "sponsoredLimit",
        args: [addr as `0x${string}`],
      })) as bigint;
      setLookupLimit(unitsToUsd(limit));
    } catch {
      setLookupLimit(null);
    }
  }, [publicClient, lookupAddr]);

  const setAdmin = useCallback(
    async (status: boolean) => {
      if (!publicClient || adminBusy) return;
      setError(undefined);
      setSuccess(undefined);
      const addr = adminAddr.trim();
      if (!/^0x[0-9a-fA-F]{40}$/.test(addr)) {
        setError("Enter a valid 0x address for the admin");
        return;
      }
      setAdminBusy(true);
      try {
        const hash = await writeContractAsync({
          address: INSTRUCTION_SENDER,
          abi: instructionSenderAbi,
          functionName: "setAdmin",
          args: [addr as `0x${string}`, status],
          chainId: activeChain.id,
        });
        await publicClient.waitForTransactionReceipt({ hash });
        setSuccess(status ? `Added ${truncAddr(addr)} as admin` : `Removed ${truncAddr(addr)} as admin`);
        setAdminAddr("");
      } catch (e) {
        setError(e instanceof Error ? e.message.split("\n")[0].slice(0, 200) : String(e));
      } finally {
        setAdminBusy(false);
      }
    },
    [publicClient, adminBusy, adminAddr, writeContractAsync],
  );

  if (checking) return null;
  if (!role) return null;

  // Collapsed state: a small chip only admins can see, to reopen the panel.
  if (!open) {
    return (
      <button
        onClick={() => setOpen(true)}
        style={css(
          "display:inline-flex;align-items:center;gap:7px;padding:8px 14px;border-radius:10px;border:1px solid rgba(124,140,255,0.3);background:rgba(124,140,255,0.08);color:#8aa0ff;font-size:12.5px;font-weight:700;cursor:pointer;margin-bottom:22px;transition:background .15s;",
        )}
      >
        🛡 Admin
      </button>
    );
  }

  const inputStyle = css(
    "flex:1;padding:10px 12px;border-radius:10px;border:1px solid rgba(255,255,255,0.1);background:rgba(0,0,0,0.3);color:#e9ecf4;font-size:13px;font-family:var(--font-jetbrains),ui-monospace,monospace;outline:none;",
  );
  const btnStyle = (disabled: boolean) =>
    css(
      `padding:10px 18px;border-radius:10px;border:none;background:${C.accent};color:${C.accentText};font-size:13px;font-weight:800;cursor:pointer;white-space:nowrap;` +
        (disabled ? "opacity:0.5;cursor:not-allowed;" : ""),
    );
  const ghostBtn = (disabled: boolean) =>
    css(
      `padding:10px 14px;border-radius:10px;border:1px solid rgba(255,255,255,0.12);background:rgba(255,255,255,0.03);color:#c8cede;font-size:12.5px;font-weight:700;cursor:pointer;white-space:nowrap;` +
        (disabled ? "opacity:0.5;cursor:not-allowed;" : ""),
    );

  return (
    <div
      style={css(
        "border-radius:18px;border:1px solid rgba(124,140,255,0.3);background:linear-gradient(180deg,rgba(124,140,255,0.06),rgba(124,140,255,0.01));padding:18px;margin-bottom:22px;animation:hypeRise 0.28s ease;",
      )}
    >
      {/* header */}
      <div style={css("display:flex;align-items:center;gap:10px;margin-bottom:16px;")}>
        <span
          style={css(
            "width:30px;height:30px;border-radius:9px;background:rgba(124,140,255,0.16);color:#8aa0ff;display:flex;align-items:center;justify-content:center;font-size:15px;",
          )}
        >
          🛡
        </span>
        <div style={css("flex:1;min-width:0;")}>
          <div style={css("font-size:14px;font-weight:800;color:#e9ecf4;")}>
            Admin — Funding Allowlist
            <span
              style={css(
                "margin-left:8px;font-size:10px;font-weight:700;padding:2px 7px;border-radius:6px;background:rgba(124,140,255,0.14);color:#8aa0ff;vertical-align:middle;",
              )}
            >
              {role === "owner" ? "OWNER" : "ADMIN"}
            </span>
          </div>
          <div style={css("font-size:12px;color:#8a93a6;")}>
            Set per-user sponsored limits. Treasury funds deposits up to the cumulative limit.
          </div>
        </div>
        <button
          onClick={() => setOpen(false)}
          style={css(
            "width:26px;height:26px;border-radius:8px;border:1px solid rgba(255,255,255,0.1);background:transparent;color:#8a93a6;font-size:13px;cursor:pointer;flex-shrink:0;",
          )}
        >
          ✕
        </button>
      </div>

      {/* set limit */}
      <div style={css("display:flex;gap:8px;margin-bottom:12px;flex-wrap:wrap;")}>
        <input
          value={targetAddr}
          onChange={(e) => setTargetAddr(e.target.value)}
          placeholder="0x… (Flare address)"
          style={inputStyle}
        />
        <input
          value={limitUsd}
          onChange={(e) => setLimitUsd(e.target.value)}
          placeholder="Limit USD (0 = revoke)"
          type="number"
          min="0"
          style={css("max-width:160px;" + Object.entries(inputStyle).map(([k, v]) => `${k}:${v}`).join(";"))}
        />
        <button onClick={setLimit} disabled={busy} style={btnStyle(busy)}>
          {busy ? "Setting…" : "Set limit"}
        </button>
      </div>

      {/* lookup */}
      <div style={css("display:flex;gap:8px;align-items:center;flex-wrap:wrap;margin-bottom:14px;")}>
        <input
          value={lookupAddr}
          onChange={(e) => {
            setLookupAddr(e.target.value);
            setLookupLimit(null);
          }}
          placeholder="Look up a user's limit…"
          style={inputStyle}
        />
        <button onClick={lookup} style={btnStyle(false)}>
          Look up
        </button>
        {lookupLimit !== null && (
          <span style={css("font-size:13px;font-weight:700;color:#8aa0ff;")}>
            {lookupLimit === 0 ? "Not sponsored" : `Limit: ${usd(lookupLimit)}`}
          </span>
        )}
      </div>

      {/* admin management — owner only */}
      {role === "owner" && (
        <div
          style={css(
            "border-top:1px solid rgba(255,255,255,0.07);padding-top:14px;margin-top:2px;",
          )}
        >
          <div style={css("font-size:12px;font-weight:700;color:#8a93a6;margin-bottom:8px;")}>
            Manage admins
          </div>
          <div style={css("display:flex;gap:8px;flex-wrap:wrap;")}>
            <input
              value={adminAddr}
              onChange={(e) => setAdminAddr(e.target.value)}
              placeholder="0x… (admin address)"
              style={inputStyle}
            />
            <button onClick={() => setAdmin(true)} disabled={adminBusy} style={ghostBtn(adminBusy)}>
              + Add admin
            </button>
            <button onClick={() => setAdmin(false)} disabled={adminBusy} style={ghostBtn(adminBusy)}>
              − Remove
            </button>
          </div>
        </div>
      )}

      {/* feedback */}
      {error && (
        <div style={css("margin-top:10px;font-size:12px;color:#ff8082;font-weight:600;")}>{error}</div>
      )}
      {success && (
        <div style={css("margin-top:10px;font-size:12px;color:#2ece6f;font-weight:600;")}>{success}</div>
      )}
    </div>
  );
}
