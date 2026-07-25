# hype-accounts frontend (plumbing spike)

A Next.js + wagmi/viem app that drives the real TEE Hyperliquid extension from a
browser wallet. This is **milestone 1** of `docs/frontend-testnet-plan.md`: prove
the full loop — sign an instruction → mine on Flare → parse `instructionId` →
poll the TEE proxy for the result → decode it.

## The loop it implements

1. Connect an injected wallet (MetaMask) on **Coston** (default) or **Coston2**.
2. Call `deposit` / `placeOrder` on the `HyperliquidInstructionSender` with the
   JSON payload as bytes and the `1000000` wei instruction fee as `msg.value`.
3. Wait for the receipt, parse `TeeInstructionsSent` → `instructionId`.
4. Poll `/api/result/{instructionId}` (a server route that proxies the TEE
   proxy's `GET /action/result/{id}`) until the status is terminal.
5. Decode `result.data` (hex JSON) and render it in a timeline.

`msg.sender` is embedded on-chain, so **your connected wallet deterministically
maps to your own derived Hyperliquid account** — no server-side custody.

## Configure

```
cp .env.local.example .env.local
```

Then set:

- `NEXT_PUBLIC_CHAIN` — `coston` (default, where the live stack is registered) or `coston2`.
- `NEXT_PUBLIC_INSTRUCTION_SENDER` — from `../config/extension.env`.
- `TEE_PROXY_URL` — **the only piece not yet known**: the public URL of the TEE
  proxy (today an ephemeral cloudflared/ngrok tunnel in front of the node).
  Server-side only; must be reachable from wherever `next` runs.

## Run

```
npm install
npm run dev
```

Open http://localhost:3000, connect a wallet funded with testnet gas
([Coston faucet](https://faucet.flare.network/coston)), and deposit.

## Notes / next milestones

- Reads (positions, balances, open orders) will come straight from Hyperliquid's
  public info API keyed on the derived HL address (milestone 2).
- Cancel/withdraw use the same `send()` path — wire buttons when needed.
- Not yet built: guardrails (per-address caps, rate limits), styling pass, deploy.
