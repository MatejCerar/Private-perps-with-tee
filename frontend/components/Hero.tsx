"use client";

import { css, C } from "@/lib/ui";

export function Hero({
  onConnect,
  connecting = false,
  error,
  hasWallet = true,
}: {
  onConnect: () => void;
  connecting?: boolean;
  error?: string;
  hasWallet?: boolean;
}) {
  return (
    <main style={css("max-width:560px;margin:0 auto;padding:96px 24px 60px;text-align:center;")}>
      <div
        style={css(
          "display:inline-flex;align-items:center;gap:7px;padding:6px 12px;border-radius:999px;border:1px solid rgba(255,255,255,0.08);background:rgba(255,255,255,0.03);font-size:12px;font-weight:600;color:#8a93a6;margin-bottom:28px;",
        )}
      >
        <span style={css("width:6px;height:6px;border-radius:50%;background:#2ece6f;")} />
        Hyperliquid testnet · powered by a secure enclave
      </div>
      <h1
        style={css(
          "font-size:42px;line-height:1.08;font-weight:800;letter-spacing:-0.03em;margin:0 0 16px;",
        )}
      >
        Leveraged trading,
        <br />
        as simple as a swap.
      </h1>
      <p
        style={css(
          "font-size:17px;line-height:1.55;color:#9aa3b6;margin:0 auto 34px;max-width:420px;",
        )}
      >
        Go long or short on real Hyperliquid testnet markets with play money. Pick a direction,
        set your leverage, done.
      </p>
      {hasWallet ? (
        <button
          onClick={onConnect}
          disabled={connecting}
          style={css(
            `display:inline-flex;align-items:center;gap:9px;padding:15px 30px;border-radius:14px;border:none;background:${C.accent};color:#0b0d13;font-size:16px;font-weight:800;cursor:${connecting ? "default" : "pointer"};box-shadow:0 10px 30px -8px rgba(124,140,255,0.5);` +
              (connecting ? "opacity:0.7;" : ""),
          )}
        >
          {connecting && (
            <span
              style={css(
                "width:15px;height:15px;border-radius:50%;border:2px solid #0b0d13;border-top-color:transparent;animation:hypeSpin .8s linear infinite;",
              )}
            />
          )}
          {connecting ? "Connecting…" : "Connect wallet"}
        </button>
      ) : (
        <a
          href="https://metamask.io/download/"
          target="_blank"
          rel="noreferrer"
          style={css(
            `display:inline-flex;align-items:center;gap:9px;padding:15px 30px;border-radius:14px;border:none;background:${C.accent};color:#0b0d13;font-size:16px;font-weight:800;cursor:pointer;box-shadow:0 10px 30px -8px rgba(124,140,255,0.5);`,
          )}
        >
          Install a wallet ↗
        </a>
      )}
      {error && (
        <div style={css("margin-top:16px;font-size:13px;color:#ff8082;font-weight:600;")}>{error}</div>
      )}
      {!hasWallet && (
        <div style={css("margin-top:14px;font-size:13px;color:#8a93a6;")}>
          No browser wallet detected — install MetaMask, then reload.
        </div>
      )}
      <div style={css("display:flex;gap:12px;margin-top:48px;text-align:left;")}>
        <Feature emoji="🪙" title="Play money" body="Test dollars from a shared treasury. Zero real value." />
        <Feature emoji="✍️" title="You stay in control" body="You sign every action with your own wallet." />
        <Feature emoji="🔒" title="~15s per action" body="Trades route through a secure enclave, then to Hyperliquid." />
      </div>
    </main>
  );
}

function Feature({ emoji, title, body }: { emoji: string; title: string; body: string }) {
  return (
    <div
      style={css(
        "flex:1;padding:18px;border-radius:14px;border:1px solid rgba(255,255,255,0.07);background:rgba(255,255,255,0.02);",
      )}
    >
      <div style={css("font-size:20px;margin-bottom:8px;")}>{emoji}</div>
      <div style={css("font-weight:700;font-size:13.5px;margin-bottom:4px;")}>{title}</div>
      <div style={css("font-size:12.5px;color:#8a93a6;line-height:1.45;")}>{body}</div>
    </div>
  );
}

export function Onboarding({ onDismiss }: { onDismiss: () => void }) {
  return (
    <div
      style={css(
        "display:flex;align-items:center;gap:16px;padding:16px 18px;border-radius:14px;border:1px solid rgba(124,140,255,0.22);background:linear-gradient(180deg,rgba(124,140,255,0.09),rgba(124,140,255,0.03));margin-bottom:22px;animation:hypeRise 0.4s ease;",
      )}
    >
      <div style={css("font-size:15px;color:#9aa3b6;line-height:1.5;flex:1;")}>
        <strong style={css("color:#e9ecf4;")}>Welcome.</strong> This is testnet play money. You
        sign each action yourself, and every trade takes ~10–20s while it routes through a secure
        enclave to Hyperliquid. <strong style={css("color:#e9ecf4;")}>Deposit to start.</strong>
      </div>
      <button
        onClick={onDismiss}
        style={css(
          "padding:8px 14px;border-radius:9px;border:1px solid rgba(255,255,255,0.12);background:transparent;color:#c8cede;font-size:13px;font-weight:700;cursor:pointer;white-space:nowrap;",
        )}
      >
        Got it
      </button>
    </div>
  );
}
