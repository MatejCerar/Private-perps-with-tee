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
          "display:inline-flex;align-items:center;gap:7px;padding:6px 12px;border-radius:999px;border:1px solid rgba(255,255,255,0.08);background:rgba(255,255,255,0.03);font-size:12px;font-weight:600;color:#777777;margin-bottom:28px;",
        )}
      >
        <span style={css("width:6px;height:6px;border-radius:50%;background:#61b854;")} />
        Private perps on Flare · trades run in a Flare TEE
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
          "font-size:17px;line-height:1.55;color:#a2a2a2;margin:0 auto 34px;max-width:420px;",
        )}
      >
        Go long or short on real Hyperliquid markets from your Flare wallet. Deposits are pooled
        for privacy and trades run inside a Flare enclave. Pick a direction, set leverage, done.
      </p>
      {hasWallet ? (
        <button
          onClick={onConnect}
          disabled={connecting}
          style={css(
            `display:inline-flex;align-items:center;gap:9px;padding:15px 30px;border-radius:14px;border:none;background:${C.accent};color:#ffffff;font-size:16px;font-weight:800;cursor:${connecting ? "default" : "pointer"};box-shadow:0 10px 30px -8px rgba(230,32,88,0.5);` +
              (connecting ? "opacity:0.7;" : ""),
          )}
        >
          {connecting && (
            <span
              style={css(
                "width:15px;height:15px;border-radius:50%;border:2px solid #ffffff;border-top-color:transparent;animation:hypeSpin .8s linear infinite;",
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
            `display:inline-flex;align-items:center;gap:9px;padding:15px 30px;border-radius:14px;border:none;background:${C.accent};color:#ffffff;font-size:16px;font-weight:800;cursor:pointer;box-shadow:0 10px 30px -8px rgba(230,32,88,0.5);`,
          )}
        >
          Install a wallet ↗
        </a>
      )}
      {error && (
        <div style={css("margin-top:16px;font-size:13px;color:#f2726c;font-weight:600;")}>{error}</div>
      )}
      {!hasWallet && (
        <div style={css("margin-top:14px;font-size:13px;color:#777777;")}>
          No browser wallet detected — install MetaMask, then reload.
        </div>
      )}
      <div style={css("display:flex;gap:12px;margin-top:48px;text-align:left;")}>
        <Feature title="Private by design" body="Your Flare wallet and your trading account cannot be linked on-chain." />
        <Feature title="You stay in control" body="You sign every action with your own wallet. Trades cost no gas." />
        <Feature title="Runs in a Flare TEE" body="Orders execute inside a Flare confidential enclave, then on Hyperliquid." />
      </div>
    </main>
  );
}

function Feature({ title, body }: { title: string; body: string }) {
  return (
    <div
      style={css(
        "flex:1;padding:18px;border-radius:14px;border:1px solid rgba(255,255,255,0.07);background:rgba(255,255,255,0.02);",
      )}
    >
      <div
        style={css(
          `width:22px;height:4px;border-radius:2px;background:linear-gradient(90deg,#e62058,#e62058);margin-bottom:12px;`,
        )}
      />
      <div style={css("font-weight:700;font-size:13.5px;margin-bottom:4px;")}>{title}</div>
      <div style={css("font-size:12.5px;color:#777777;line-height:1.45;")}>{body}</div>
    </div>
  );
}

export function Onboarding({ onDismiss }: { onDismiss: () => void }) {
  return (
    <div
      style={css(
        "display:flex;align-items:center;gap:16px;padding:16px 18px;border-radius:14px;border:1px solid rgba(230,32,88,0.22);background:linear-gradient(180deg,rgba(230,32,88,0.09),rgba(230,32,88,0.03));margin-bottom:22px;animation:hypeRise 0.4s ease;",
      )}
    >
      <div style={css("font-size:15px;color:#a2a2a2;line-height:1.5;flex:1;")}>
        <strong style={css("color:#f6f6f6;")}>Welcome.</strong> This is testnet play money. You
        sign each action yourself, and every trade takes ~10–20s while it routes through a secure
        enclave to Hyperliquid. <strong style={css("color:#f6f6f6;")}>Deposit to start.</strong>
      </div>
      <button
        onClick={onDismiss}
        style={css(
          "padding:8px 14px;border-radius:9px;border:1px solid rgba(255,255,255,0.12);background:transparent;color:#d1d1d1;font-size:13px;font-weight:700;cursor:pointer;white-space:nowrap;",
        )}
      >
        Got it
      </button>
    </div>
  );
}
