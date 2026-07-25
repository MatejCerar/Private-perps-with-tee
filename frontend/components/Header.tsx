"use client";

import { useState } from "react";
import { css, C } from "@/lib/ui";
import {
  activeChain,
  faucetUrl,
  explorerAddress,
  hlExplorerAddress,
} from "@/lib/config";

const chainShort = activeChain.name.replace(/^Flare\s+/, "");

function truncMid(a: string): string {
  return a.length > 12 ? `${a.slice(0, 6)}…${a.slice(-4)}` : a;
}

export function Header(props: {
  connected: boolean;
  wrongNetwork: boolean;
  balanceStr: string;
  walletAddr: string;
  flareAddress?: string;
  hlAddress?: string;
  onSwitch: () => void;
  onDeposit: () => void;
  onDisconnect: () => void;
}) {
  return (
    <header
      style={css(
        "position:sticky;top:0;z-index:40;display:flex;align-items:center;gap:16px;padding:14px 24px;background:rgba(8,10,15,0.82);backdrop-filter:blur(12px);border-bottom:1px solid rgba(255,255,255,0.07);",
      )}
    >
      <div style={css("display:flex;align-items:center;gap:10px;margin-right:auto;")}>
        <div
          style={css(
            `width:28px;height:28px;border-radius:8px;background:${C.accent};display:flex;align-items:center;justify-content:center;font-weight:800;color:#0b0d13;font-size:15px;`,
          )}
        >
          h
        </div>
        <span style={css("font-weight:800;font-size:16px;letter-spacing:-0.02em;")}>
          hype<span style={css("color:#8a93a6;font-weight:600;")}>-accounts</span>
        </span>
      </div>

      {props.connected && (
        <div style={css("display:flex;align-items:center;gap:12px;")}>
          {props.wrongNetwork ? (
            <button
              onClick={props.onSwitch}
              style={css(
                "display:flex;align-items:center;gap:7px;padding:8px 12px;border-radius:10px;border:1px solid rgba(227,173,70,0.4);background:rgba(227,173,70,0.12);color:#e3ad46;font-size:13px;font-weight:700;cursor:pointer;",
              )}
            >
              Switch to {chainShort}
            </button>
          ) : (
            <div
              style={css(
                "display:flex;align-items:center;gap:7px;padding:8px 12px;border-radius:10px;border:1px solid rgba(255,255,255,0.09);background:rgba(255,255,255,0.03);color:#c8cede;font-size:13px;font-weight:600;",
              )}
            >
              <span
                style={css(
                  "width:7px;height:7px;border-radius:50%;background:#2ece6f;box-shadow:0 0 0 3px rgba(46,206,111,0.18);",
                )}
              />
              {chainShort}
            </div>
          )}

          <button
            onClick={props.onDeposit}
            style={css(
              "display:flex;flex-direction:column;align-items:flex-end;gap:1px;padding:6px 14px;border-radius:10px;border:1px solid rgba(255,255,255,0.09);background:rgba(255,255,255,0.03);cursor:pointer;",
            )}
          >
            <span
              style={css(
                "font-size:10px;color:#8a93a6;font-weight:600;letter-spacing:0.04em;text-transform:uppercase;",
              )}
            >
              Balance
            </span>
            <span style={css("font-size:15px;font-weight:800;color:#e9ecf4;")}>
              {props.balanceStr}
            </span>
          </button>

          <WalletMenu
            walletAddr={props.walletAddr}
            flareAddress={props.flareAddress}
            hlAddress={props.hlAddress}
            onDisconnect={props.onDisconnect}
          />
        </div>
      )}
    </header>
  );
}

function WalletMenu(props: {
  walletAddr: string;
  flareAddress?: string;
  hlAddress?: string;
  onDisconnect: () => void;
}) {
  const [open, setOpen] = useState(false);
  const [copied, setCopied] = useState<"flare" | "hl" | null>(null);

  const copy = (which: "flare" | "hl", value?: string) => {
    if (!value || !navigator.clipboard) return;
    navigator.clipboard
      .writeText(value)
      .then(() => {
        setCopied(which);
        setTimeout(() => setCopied(null), 1200);
      })
      .catch(() => {});
  };

  return (
    <div style={css("position:relative;")}>
      <button
        onClick={() => setOpen((v) => !v)}
        style={css(
          "display:flex;align-items:center;gap:8px;padding:9px 12px;border-radius:10px;border:1px solid rgba(255,255,255,0.09);background:rgba(255,255,255,0.03);color:#e9ecf4;font-size:13px;font-weight:700;cursor:pointer;font-family:var(--font-jetbrains),ui-monospace,monospace;",
        )}
      >
        <span
          style={css(
            "width:18px;height:18px;border-radius:50%;background:conic-gradient(from 210deg,#7c8cff,#2ece6f,#ff5d5f,#7c8cff);",
          )}
        />
        {props.walletAddr}
        <span
          style={css(
            "color:#8a93a6;display:inline-block;transition:transform .2s;" +
              (open ? "transform:rotate(180deg);" : ""),
          )}
        >
          ▾
        </span>
      </button>

      {open && (
        <>
          <div onClick={() => setOpen(false)} style={css("position:fixed;inset:0;z-index:45;")} />
          <div
            style={css(
              "position:absolute;top:calc(100% + 8px);right:0;z-index:55;width:308px;padding:8px;border-radius:14px;border:1px solid rgba(255,255,255,0.1);background:#161b26;box-shadow:0 20px 44px -18px rgba(0,0,0,0.9);animation:hypeRise 0.16s ease;",
            )}
          >
            {/* Flare wallet */}
            <div style={css("padding:10px 10px 12px;")}>
              <div
                style={css(
                  "font-size:10px;color:#8a93a6;font-weight:700;letter-spacing:0.06em;text-transform:uppercase;margin-bottom:8px;",
                )}
              >
                Flare wallet · {chainShort}
              </div>
              <AddressRow
                address={props.flareAddress}
                copied={copied === "flare"}
                onCopy={() => copy("flare", props.flareAddress)}
              />
              {props.flareAddress && (
                <ExplorerLink
                  href={explorerAddress(props.flareAddress)}
                  label="View on Flare explorer"
                />
              )}
            </div>

            <div style={css("height:1px;background:rgba(255,255,255,0.07);margin:2px 4px;")} />

            {/* Hyperliquid account (derived — a different address) */}
            <div style={css("padding:10px 10px 12px;")}>
              <div
                style={css(
                  "font-size:10px;color:#8a93a6;font-weight:700;letter-spacing:0.06em;text-transform:uppercase;margin-bottom:6px;",
                )}
              >
                Hyperliquid account
              </div>
              <div style={css("font-size:11px;color:#6f7789;line-height:1.45;margin-bottom:8px;")}>
                Derived from your wallet by the TEE — a different address that holds your
                positions.
              </div>
              {props.hlAddress ? (
                <>
                  <AddressRow
                    address={props.hlAddress}
                    copied={copied === "hl"}
                    onCopy={() => copy("hl", props.hlAddress)}
                  />
                  <ExplorerLink
                    href={hlExplorerAddress(props.hlAddress)}
                    label="View on Hyperliquid explorer"
                  />
                </>
              ) : (
                <div style={css("font-size:12px;color:#8a93a6;")}>
                  Created on your first deposit.
                </div>
              )}
            </div>

            <div style={css("height:1px;background:rgba(255,255,255,0.07);margin:2px 4px;")} />

            <button
              onClick={() => {
                setOpen(false);
                props.onDisconnect();
              }}
              style={css(
                "display:flex;align-items:center;justify-content:center;gap:8px;width:100%;padding:10px;border-radius:10px;border:none;background:rgba(255,93,95,0.12);color:#ff8082;font-size:13px;font-weight:800;cursor:pointer;margin-top:4px;",
              )}
            >
              Disconnect
            </button>
          </div>
        </>
      )}
    </div>
  );
}

function AddressRow({
  address,
  copied,
  onCopy,
}: {
  address?: string;
  copied: boolean;
  onCopy: () => void;
}) {
  return (
    <div style={css("display:flex;align-items:center;justify-content:space-between;gap:8px;margin-bottom:8px;")}>
      <span
        style={css(
          "font-family:var(--font-jetbrains),ui-monospace,monospace;font-size:12.5px;color:#e9ecf4;",
        )}
      >
        {address ? truncMid(address) : "—"}
      </span>
      {address && (
        <button
          onClick={onCopy}
          style={css(
            "padding:4px 9px;border-radius:7px;border:1px solid rgba(255,255,255,0.1);background:transparent;color:" +
              (copied ? C.green : "#8a93a6") +
              ";font-size:11px;font-weight:700;cursor:pointer;white-space:nowrap;",
          )}
        >
          {copied ? "Copied ✓" : "Copy"}
        </button>
      )}
    </div>
  );
}

function ExplorerLink({ href, label }: { href: string; label: string }) {
  return (
    <a
      href={href}
      target="_blank"
      rel="noreferrer"
      style={css(
        "display:inline-flex;align-items:center;gap:5px;font-size:12px;font-weight:700;color:#8aa0ff;",
      )}
    >
      {label} ↗
    </a>
  );
}

export function Banner() {
  return (
    <div
      style={css(
        "display:flex;align-items:center;gap:10px;padding:9px 24px;background:rgba(227,173,70,0.09);border-bottom:1px solid rgba(227,173,70,0.16);color:#e3ad46;font-size:12.5px;font-weight:600;",
      )}
    >
      <span style={css("font-size:13px;")}>⚠</span>
      <span style={css("color:#e8c885;")}>
        Testnet — deposits are mock-funded from a shared treasury. No real value, nothing at
        risk.
      </span>
      <a
        href={faucetUrl()}
        target="_blank"
        rel="noreferrer"
        style={css("margin-left:auto;color:#e3ad46;font-weight:700;font-size:12px;")}
      >
        Get test gas ↗
      </a>
    </div>
  );
}
