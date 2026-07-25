# Frontend Testnet Playground — Plan

> A public web app where users test the Hyperliquid extension on **Flare testnet**,
> with **bridging mocked** (treasury-funded deposits). Decisions locked:
> **treasury mode** · **users' own Flare wallet** · **Next.js + wagmi/viem on Vercel**.
> Companion to [`architecture.md`](architecture.md) and [`STATUS.md`](STATUS.md).

## 1. Goal & what's real vs mocked

A user connects their Flare wallet, "deposits" (mock testnet money), trades perps/spot
on **real Hyperliquid testnet**, sees live positions, cancels, and withdraws. The whole
TEE instruction pipeline (on-chain → data-provider relay → TEE proxy → extension → HL) is
**real**; only the money getting *onto* HL is faked.

| Piece | Real or mocked | Why |
|---|---|---|
| Wallet connect + on-chain instruction | **real** | user signs `InstructionSender.deposit/placeOrder/cancel/withdraw` with their own key |
| Data-provider relay + TEE proxy + extension | **real** | this is the product; it's what we're demoing |
| HL trading (perp/spot, market/limit, cancel, leverage) | **real** (HL testnet) | already proven (C1/C3/C8) |
| **Funding / bridge (Flare→Arb→HL)** | **MOCKED** | treasury mode: deposit = HL `spotSend` from a shared treasury, no real bridge |
| Withdraw | **mocked settlement** | treasury mode: `spotSend` back to the treasury (funds recycle) |

Treasury mode is ideal for a public test: **no real value is at risk** (testnet only), the
treasury recycles on withdraw, and it's the exact path proven end-to-end on Coston (C8).

## 2. The three interfaces the frontend uses

**Write — one on-chain tx per action** to `HyperliquidInstructionSender`
(`contracts/InstructionSender.sol`). The contract embeds `msg.sender` as the user, so a
user's own wallet deterministically maps to their own derived HL account. Payloads:

- `deposit(bytes)` → `{"usdAmount":100}`
- `placeOrder(bytes)` → `{"market":"perp","coin":"ETH","isBuy":true,"orderType":"market","size":0.01,"leverage":3}` (limit adds `"orderType":"limit","limitPx":1800`; `"reduceOnly"`, `"isCross"` optional)
- `cancel(bytes)` → `{"coin":"ETH","orderId":123}`
- `withdraw(bytes)` → `{"usdAmount":50}`

Each call is `payable` and must forward the registry instruction fee as `msg.value`.

**Read #1 — instruction result** (async). From the tx receipt, parse the
`TeeInstructionsSent` event → `instructionId`. Poll the public proxy
`GET /action/result/{instructionId}` until `status != 2` (2=pending, 1=ok, 0=fail).
`Result.Data` is the typed response (`DepositResponse.hlAddress`, `OrderResponse.orderId`, …).

**Read #2 — live account truth.** After the first deposit the derived **HL address** is
known. Query **Hyperliquid's public info API** directly for balances, positions, open
orders, and mids — no TEE round-trip needed. (The extension's `GET /state` ledger is just
its internal mirror; the frontend doesn't need it.)

Optional: the **types-server** `POST /decode` renders raw hex payloads/results as JSON for
a "watch the pipeline" debug view.

## 3. Architecture

```
Browser (Next.js on Vercel)
  │  wagmi/viem — user signs
  ▼
Flare testnet (Coston2)  ──▶  HyperliquidInstructionSender  ──▶ TeeExtensionRegistry
                                                                     │ relay + cosign
  ┌───────────── Next.js route handlers (server-side, no CORS) ──────┼─────────────┐
  │  GET /api/result/[id]  ──▶  TEE proxy /action/result/{id}        ▼             │
  │  GET /api/hl/*         ──▶  Hyperliquid testnet info API   TEE proxy → extension │
  └──────────────────────────────────────────────────────────  (treasury credit)   ┘
                                                                     │
                                                              Hyperliquid testnet
```

Route reads through **Next.js API handlers** (server→server) to avoid browser CORS and to
keep the upstream proxy URL server-side. Writes go straight from the browser wallet.

## 4. Prerequisites / infra (do these first — they gate a hosted frontend)

1. **Stable public proxy URL.** Today `EXT_PROXY_URL` is an ephemeral ngrok/cloudflared
   quick tunnel. A hosted frontend needs a durable HTTPS endpoint for the TEE proxy
   (named cloudflare tunnel, or a small always-on reverse proxy). Blocking for deploy.
2. **Published contract facts:** InstructionSender address (deployed at pre-build, *not* in
   `config/coston2/deployed-addresses.json`), extension ID, chain ID / RPC
   (`https://coston2-api.flare.network/ext/C/rpc`), and the current instruction **fee**
   (read from the registry or configured). Frontend ships a tiny `config.ts` with these.
3. **Extension running in treasury mode**, registered, treasury funded on HL testnet with
   enough USDC to cover expected concurrent test deposits.
4. **Network choice:** target **Coston2** (public faucet for C2FLR). Confirm the extension
   is registered on the same network the frontend points at.
5. **CORS** on the proxy's `/action/result/*` *if* we ever read it from the browser —
   avoided by routing through Next.js API handlers (preferred).

## 5. Feature scope (v1)

- **Connect + onboarding:** connect Coston2 wallet; "Get testnet gas" faucet link; a
  first-run explainer of real-vs-mocked; persistent **testnet + mocked-bridge** banners.
- **Deposit (mock):** amount input with a **per-address cap**; on success show the derived
  HL address + a link to the HL testnet explorer. Copy makes clear it's treasury-funded.
- **Trade panel:** perp & spot, market & limit, size, leverage, reduce-only; show live mid
  to guide limit price; validate size against HL `szDecimals` (e.g. PURR needs whole
  numbers) so orders aren't rejected.
- **Portfolio:** balance, open positions (with unrealized PnL), open orders — polled live
  from HL — each order with a **Cancel** button.
- **Withdraw:** amount input; note the ~1 USDC HL fee and settlement time.
- **Instruction timeline:** per-action status card (submitted → relayed → executed/failed)
  driven by the result poll. This is the demo's "wow" — it makes the TEE pipeline legible.

Out of scope v1: order book depth, charts, cross/isolated margin editing beyond leverage,
multi-network switching.

## 6. UX realities to design around

- **Latency:** data-provider relay adds ~12s before the extension even sees an instruction;
  treasury deposits/orders then resolve in seconds. Every action is async → optimistic
  "pending" UI + the timeline component, never a blocking spinner.
- **Gas:** users pay the instruction fee in C2FLR from their own wallet → faucet link +
  a clear "insufficient gas" state.
- **First-order sequencing:** the account is switched to HL **unified** mode only *after*
  the first deposit credits — so gate trading UI until the first deposit result returns an
  HL address.
- **Failures are normal:** HL rejections (min size, price too far, insufficient margin) and
  relay timeouts must surface as readable messages from `Result.Log`, not silent failures.

## 7. Guardrails for a public test

- **Per-address deposit cap** (e.g. $100) + optional one-deposit-per-address; enforced in
  UI and backed by the extension's `HL_MAX_DEPOSIT_USD` / total cap.
- **Rate limiting** on the Next.js result/HL proxy routes.
- **Treasury monitoring:** alert when treasury USDC drops below a floor; runbook to refill.
- **Optional access gate** (invite code / allowlist) for a controlled first cohort.
- Because it's treasury mode on testnet, worst-case abuse is treasury depletion (refillable),
  not loss of real funds.

## 8. Milestones

1. **Plumbing spike** — Next.js + wagmi connect to Coston2; call `deposit` from a browser
   wallet; parse `TeeInstructionsSent`; poll the proxy via a Next.js route; render the raw
   result. Proves the full loop, flushes out the stable-proxy-URL and fee/address deps.
2. **Read layer** — HL info-API client (balances, positions, open orders, mids, meta) keyed
   on the derived address; polling hooks; Next.js proxy routes.
3. **Core UI** — deposit → trade → portfolio → cancel → withdraw + the instruction timeline.
4. **Guardrails** — caps, rate limits, treasury monitor, error surfaces, banners, onboarding.
5. **Deploy + harden** — Vercel deploy, stable public proxy endpoint, treasury top-up runbook,
   smoke test with a fresh wallet end-to-end.

## 9. Open items to confirm before build

- Stable public URL for the TEE proxy (infra owner + method).
- Where the InstructionSender address + extension ID + fee are sourced at build time.
- Per-address cap value and whether v1 is open or invite-gated.
- Whether to expose the types-server decode view (nice-to-have debug panel).
