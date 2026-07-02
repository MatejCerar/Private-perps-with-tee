# Hyperliquid Controlled Accounts — Design & Implementation Guide

This document explains the Hyperliquid-specific parts of the `hype-accounts` extension:
what it does, how it works, the platform issues we hit and solved on Hyperliquid testnet,
how to test it, and what is still missing. For generic scaffold/extension mechanics see
[`extension-guide.md`](extension-guide.md); for the project README and operator runbook see
[`../README.md`](../README.md).

> **Status (2026-07-02):** the Hyperliquid trading path is **validated live on testnet** at
> both the adapter and the real-handler level. The on-chain instruction path (Flare contract →
> data-provider relay → TEE proxy → handler) is implemented but **not yet run end-to-end**.
> See [§9 What's validated / what's missing](#9-whats-validated--whats-missing).

---

## 1. What this extension does

Users trade on Hyperliquid (spot + perpetuals) by sending **plain on-chain instructions on
Flare**. A TEE holds a **dedicated, isolated Hyperliquid account per user**, executes the
requested action on Hyperliquid's real testnet, and the user never bridges funds off Flare.

The UX target is AMM-simple: users think in "deposit / trade / positions", not order books.
Limit orders are supported under the hood, but the surfaced model hides order-book mechanics.

Design decisions locked during brainstorming (see
[`superpowers/specs/2026-06-30-hyperliquid-controlled-accounts-design.md`](superpowers/specs/2026-06-30-hyperliquid-controlled-accounts-design.md)):

- **Per-user accounts**, not a pooled/omnibus account — so Hyperliquid does all the
  accounting, margining, and liquidation per user natively. This avoids building a broker
  with cross-user liquidation risk inside the TEE.
- **Spot + perps** from day one.
- **Mock deposit** in v1 — a `DEPOSIT` instruction credits the user and funds them from a
  treasury; no real FXRP moves yet.
- **Market + limit** orders.
- **No FSA yet** — users are identified by the EOA that calls the contract.

---

## 2. End-to-end architecture

```
User EOA ──tx──► HyperliquidInstructionSender.sol (Flare / Coston2)
                   │ emits instruction, message = abi.encode(msg.sender, jsonPayload)
                   ▼
        Data providers relay + threshold-sign the instruction
                   ▼
              TEE proxy (action queue)
                   ▼
   TEE machine ── extension.processAction(opCommand)
        │  1. recover user  = DecodeEnvelope(message).address   (on-chain authenticated)
        │  2. derive HL key = HKDF(masterSeed, "hl-account:"+user)
        │  3. build a go-hyperliquid Exchange for that key
        │  4. fund / transfer / order / cancel on Hyperliquid testnet
        │  5. update the in-memory ledger
        ▼
   Signed ActionResult ──► proxy (clients poll the result)
```

Hyperliquid REST calls are slow, so the extension uses the scaffold's **async pattern**:
`handleAsync` immediately returns a `pending` (status 2) result, then a goroutine runs the
handler and POSTs the real result to the tee-node `/result` port.

---

## 3. Identity model

The on-chain contract emits `message = abi.encode(msg.sender, jsonPayload)`. Because data
providers relay exactly what the contract emitted, the embedded address is trustworthy — a
user cannot act on behalf of another user.

- Contract: `contracts/InstructionSender.sol`, `_send(opCommand, payload)` builds the envelope.
- TEE: `pkg/types.DecodeEnvelope(message) (common.Address, []byte, error)` splits it back into
  `(user, jsonPayload)`. The ABI layout is `(address, bytes)` — address first — and must match
  the contract exactly.

There is **no payload encryption** — the TEE owns all keys and no user secret ever enters the
system.

---

## 4. Per-user account model

### Key derivation (`internal/keys`)

Each user's Hyperliquid signing key is derived deterministically:

```
userHLKey = HKDF-SHA256(masterSeed, info = "hl-account:" + lowercase(userFlareAddr))
```

- Deterministic: same `(masterSeed, user)` always yields the same key — no per-user key
  storage, recoverable from the seed.
- `crypto.ToECDSA` validates the scalar; a counter byte in the info string retries on the
  (astronomically rare) invalid-scalar case.
- The TEE *is* the account holder, so it can both trade and transfer — no separate HL agent
  wallet is needed.

The master seed comes from `HL_MASTER_SEED` (env). In a later milestone this becomes a
PMW/Shamir-managed key.

### The treasury

A single treasury Hyperliquid account funds all users. It is seeded once from the testnet
faucet (1,000 mock USDC). On `DEPOSIT`, the TEE transfers USDC treasury → user's derived
account. See [§7 Funding](#7-the-funding-model-and-the-unified-account-saga).

### Ledger (`internal/ledger`)

An in-memory, mutex-guarded ledger tracks, per Flare user: USD balance credited and recent
orders. It is intentionally **notional** — it records what was credited/traded, and can drift
from real HL balances after P&L (documented limitation, see [§9](#9-whats-validated--whats-missing)).

---

## 5. Instruction set

One OPType `HL`, sub-routed by OPCommand. All four contract functions take `bytes calldata
_payload` (JSON) and wrap it as `abi.encode(msg.sender, _payload)`.

| OPCommand | Contract fn | JSON payload | Handler action |
|---|---|---|---|
| `DEPOSIT` | `deposit(bytes)` | `{"usdAmount": 30}` | cap-check → treasury `spotSend`s USDC to the user's derived account → ledger credit |
| `PLACE_ORDER` | `placeOrder(bytes)` | `{"market":"perp"\|"spot","coin":"ETH","isBuy":true,"orderType":"market"\|"limit","size":0.02,"limitPx":0,"reduceOnly":false,"leverage":2,"isCross":false}` | sweep collateral to order's side → set leverage (perp) → place order → record |
| `CANCEL` | `cancel(bytes)` | `{"coin":"ETH","orderId":123}` | cancel the order on HL |
| `WITHDRAW` | `withdraw(bytes)` | `{"usdAmount": 20}` | ledger debit → user `spotSend`s back to treasury (**stub — no FXRP back to Flare yet**) |

Handlers live in `internal/extension/processors.go`; routing in `internal/extension/extension.go`
(`processAction`). Each handler decodes the envelope, validates, executes via the HL adapter,
and returns an `ActionResult` (`status 1` ok, `status 0` error, `status 2` pending).

---

## 6. The Hyperliquid adapter (`internal/hl`)

The adapter wraps `github.com/sonirico/go-hyperliquid` v0.33.1 behind a small `Account`
interface so the handlers can be unit-tested with fakes:

```go
type Account interface {
    Address() string
    PlaceOrder(ctx, OrderReq) (OrderResult, error)   // market/limit, spot/perp; market+reduceOnly -> MarketClose
    Cancel(ctx, coin, oid) error
    SendUSD(ctx, dest, amount) error                 // spotSend of USDC (see §7)
    ClassTransfer(ctx, amount, toPerp) error         // usdClassTransfer spot<->perp
    SpotUSDC(ctx) (float64, error)
    PerpWithdrawable(ctx) (float64, error)
    SetLeverage(ctx, coin, leverage, isCross) error
    SetAbstraction(ctx, mode) error                  // userSetAbstraction (raw; see §7)
    Positions(ctx) (PositionsSnapshot, error)
}
```

`NewRealFactory(baseURL)` builds real accounts; `FakeAccount`/`FakeFactory` back the unit
tests. Compile-time assertions (`var _ Account = (*realAccount)(nil)`) guard the real impl.

### go-hyperliquid quirks we had to work around

These only surfaced against the live testnet — mocked tests could never have caught them:

1. **Metadata panic.** `NewExchange` builds a spot-asset map and does
   `spotMeta.Tokens[spotInfo.Tokens[0]]`, treating a token *index* as a positional index. On
   testnet, frozen/removed tokens leave gaps, so some pair references an index past the end of
   the slice → `index out of range` panic on *every* account construction. Fix: the adapter
   pre-fetches spot metadata (`sanitizedSpotMeta`) and drops the malformed pairs before handing
   it to `NewExchange`.
2. **Swallowed transfer errors.** `UsdTransfer`/`SpotTransfer`/`UsdClassTransfer` do not inspect
   the response status, so a *rejected* transfer returns `(resp, nil)` — a silent failure. The
   adapter checks `resp.Status` and surfaces the real error.
3. **Missing actions.** `userSetAbstraction` (account-mode toggle) is newer than v0.33.1, so the
   adapter sends it as a **raw signed action** using the library's exported
   `SignUserSignedAction` + a direct POST to `/exchange`.

---

## 7. The funding model, and the unified-account saga

This was the hard part, and it changed the funding mechanics from the original design.

### What we discovered (in order, from live errors)

1. Placing orders from a funded account **works**.
2. But funding a fresh per-user account **failed**. `usdSend` (the original plan) was rejected;
   so was `spotSend`, with an opaque empty error.
3. Surfacing the raw response revealed: **`"Action disabled when unified account is active"`**.
   The treasury was in Hyperliquid's **unified account** mode, which merges spot+perp into one
   balance and **disables inter-account transfers** (`usdSend`/`spotSend`).
4. Hyperliquid accounts have an **abstraction mode** (`userAbstraction` info query returns
   `unifiedAccount` / `portfolioMargin` / `disabled` / `default`). It's set with the
   **`userSetAbstraction`** action (`abstraction: "disabled"` = standard mode).
5. Switching to standard mode is **rejected while the account has open positions/orders**
   (`"Cannot disable unified account with open positions..."`).

### The resulting model

- The TEE puts the **treasury** in **standard mode** (`disabled`) via `SetAbstraction`. Standard
  mode keeps separate spot/perp balances and permits `spotSend` + `spot↔perp` class transfers.
  This runs best-effort at startup (`extension.New`); it fails only if the treasury has open
  positions, so the treasury must be kept flat (it is a funding account, not a trading account).
- Fresh **user accounts** come up in `default` mode, which already permits transfers and class
  transfers — no change needed.
- **Funding uses `spotSend`** (USDC token transfer), which credits the recipient's **spot**
  balance. (The USDC token id is discovered from spot metadata: `usdcTokenID` → `"USDC:<id>"`.)
- Because spot and perp balances are separate in standard mode, the **order handler sweeps
  collateral to the side the order needs**, via `usdClassTransfer`:
  - **perp order** → sweep the whole spot balance `spot → perp` (then set leverage, place).
  - **spot buy** → sweep free perp margin `perp → spot`.

  Sweeping the *entire* balance keeps each per-user account single-context and avoids
  pre-trade notional/price estimation. (Refinement noted in [§9](#9-whats-validated--whats-missing).)

### Why not a pooled account?

A single pooled account would sidestep transfers entirely and fits unified accounts naturally —
but it would force the TEE to become a broker: attributing fills, funding, and **liquidations**
across commingled positions, with one user's liquidation able to eat others' funds. The
per-user model keeps all of that native to Hyperliquid, so we kept it and solved funding instead.

---

## 8. Testing

All live tests are gated behind the `hltestnet` build tag and **skip** unless
`HL_TREASURY_KEY` and `HL_MASTER_SEED` are set, so they never run in normal CI.

### Unit tests (no network)

```
go test ./internal/... ./pkg/...
```

Cover key derivation, ledger math, envelope decode, the four handlers (with fakes, including
the sweep logic), routing, and async plumbing.

### Live tests against Hyperliquid testnet

Set the two env vars (private key **without** `0x`, seed ≥ 32 bytes hex). The repo reads them
from your shell, not from `.env`:

```
export $(grep -E '^(HL_TREASURY_KEY|HL_MASTER_SEED)=' .env | xargs)
```

| Test | Package | What it proves |
|---|---|---|
| `TestHLTestnetRoundTrip` | `./testing/` | adapter round-trip: treasury `spotSend` → user → `spot→perp` transfer → perp order → positions |
| `TestLiveDepositThenPerpOrder` | `./internal/extension/` | the **real handlers** `processDeposit` + `processPlaceOrder` end-to-end (incl. sweep-all) |
| `TestSetStandardThenSpotSend` | `./testing/` | closes positions → `SetAbstraction("disabled")` → `spotSend` succeeds |
| `TestTreasuryPlaceOrder` | `./testing/` | core order path from the treasury account |
| `TestFundTreasuryPerp` | `./testing/` | operator helper: move treasury USDC spot→perp (`HL_MOVE_TO_PERP=100`) |
| `TestSpotSendRaw` | `./testing/` | diagnostic: prints HL's raw `/exchange` response (how we found the unified-account error) |

Example — the full handler-level path:

```
export $(grep -E '^(HL_TREASURY_KEY|HL_MASTER_SEED)=' .env | xargs) && go test -tags hltestnet ./internal/extension/ -run TestLiveDepositThenPerpOrder -v
```

### Verifying independently on Hyperliquid

The tests report order ids and addresses; confirm them against HL's public info API (no key
needed):

```
curl -s -X POST https://api.hyperliquid-testnet.xyz/info -H "Content-Type: application/json" -d '{"type":"clearinghouseState","user":"0x<derived-user-addr>"}'
curl -s -X POST https://api.hyperliquid-testnet.xyz/info -H "Content-Type: application/json" -d '{"type":"userFills","user":"0x<derived-user-addr>"}'
curl -s -X POST https://api.hyperliquid-testnet.xyz/info -H "Content-Type: application/json" -d '{"type":"userAbstraction","user":"0x<addr>"}'
```

---

## 9. What's validated / what's missing

### Validated live on Hyperliquid testnet

- Full **deposit → spot→perp sweep → perp order** path through the **real handlers**; real
  orders filling on isolated per-user accounts funded from the treasury.
- The account-mode toggle (`userSetAbstraction`) and standard-mode funding via `spotSend`.
- go-hyperliquid workarounds (metadata sanitize, transfer-error surfacing).

### Not yet done

- **On-chain e2e.** The full path `HyperliquidInstructionSender.sol` → data-provider relay →
  TEE proxy → handler has not been run end-to-end. The Go handlers are proven; the on-chain
  instruction relay and the contract emitting the envelope are not yet exercised. Needs the
  full Flare stack (`scripts/full-setup.sh` + the `e2e/` infra).
- **Spot trading live.** The spot order path (and its `perp→spot` sweep) is unit-tested and
  coded but not yet exercised live; only perp orders have been run on testnet.
- **`WITHDRAW` is a stub** — it settles back to the treasury and debits the ledger; it does not
  return FXRP to Flare.
- **Real FXRP.** Deposits are *mock*: the amount is asserted by the instruction and not backed
  by real FXRP. Bounded by `HL_MAX_DEPOSIT_USD` / `HL_MAX_TOTAL_DEPOSIT_USD` and the treasury
  balance. No FXRP→FUSD conversion.
- **FSA** (Flare Smart Accounts) — users are still raw EOAs.
- **Multi-machine key consistency.** Each TEE machine derives *different* per-user keys, but the
  contract routes to a random machine. v1 assumes a **single TEE machine**. Multi-machine needs
  sticky per-user routing or PMW-shared key custody.

### Known rough edges (tracked)

- **Sweep-all collateral.** The handler moves the *entire* spot balance into perp for a perp
  order (and vice-versa for a spot buy). Simple and correct for single-context accounts; a user
  running both spot and perp will see funds shuttled each order. A future refinement can move
  only the order's required margin (needs a pre-trade price/notional estimate).
- **Notional ledger drift.** The ledger records credited/traded amounts, not live P&L, so it can
  diverge from real HL balances after trading. A `WITHDRAW` debits the ledger but the real
  transfer can fail if the account traded down (it is refunded on failure — safe, just
  surprising).
- **Treasury capacity.** The faucet gives ~1,000 mock USDC one-time per address; total demo
  capital is capped by the treasury balance.

---

## 10. Operational setup (quick reference)

Environment variables (see `.env.example`; secrets stay empty in the template):

| Var | Meaning |
|---|---|
| `HYPERLIQUID_URL` | `https://api.hyperliquid-testnet.xyz` (signing domain is chosen by this URL) |
| `HL_MASTER_SEED` | ≥32-byte hex seed for per-user key derivation (**secret**) |
| `HL_TREASURY_KEY` | private key (hex, no `0x`) of the faucet-funded treasury (**secret**) |
| `HL_MAX_DEPOSIT_USD` | per-deposit cap (default 1000) |
| `HL_MAX_TOTAL_DEPOSIT_USD` | global credited cap (default 5000) |

One-time treasury setup:

1. Fund the `HL_TREASURY_KEY` address with mock USDC from
   [app.hyperliquid-testnet.xyz/drip](https://app.hyperliquid-testnet.xyz/drip) (needs a $5
   mainnet activation of that address — a Hyperliquid requirement).
2. Keep the treasury **flat** (no open positions) so the extension can put it in standard mode.
   The extension does this best-effort at startup; you can also verify/force it with the
   `TestSetStandardThenSpotSend` helper.

See [`../README.md`](../README.md) for the deployment runbook and the instruction payload
reference.

## 11. On-chain e2e on Coston2

Sections 1–10 cover the extension and its Hyperliquid side, validated at the handler level.
This section is the runbook for driving the **full Flare stack** on Coston2: a real on-chain
transaction for each of `DEPOSIT → PLACE_ORDER (perp) → PLACE_ORDER (spot) → WITHDRAW`,
relayed and threshold-signed by the live Coston2 data providers, executed by the extension,
and verified back on Hyperliquid.

> **Model (v1):** deposit carries a **trusted amount** — no token moves on Flare; the TEE
> funds your derived HL account from the treasury. Withdraw debits the ledger and returns
> USDC to the treasury on HL — nothing is returned to your Flare address. "Mock USDC" means
> fictional on the Flare side; the only real money movement is on HL testnet.

### Prerequisites

- A **funded Coston2 key** for `DEPLOYMENT_PRIVATE_KEY` (gas): [faucet](https://faucet.flare.network/coston2).
- A **faucet-seeded, flat treasury** (`HL_TREASURY_KEY`) with ≥100 mock USDC and **no open
  positions** (see §10) — the startup `setAbstraction("disabled")` fails otherwise.
- **ngrok** so the data providers can reach your extension proxy: `ngrok http 6674`.

### Configure (operator-edited, secret-bearing files)

Fill `.env` from `.env.example` (`DEPLOYMENT_PRIVATE_KEY`, `INITIAL_OWNER`, `HL_MASTER_SEED`,
`HL_TREASURY_KEY`, `EXT_PROXY_URL=<ngrok>`; confirm `CHAIN_URL` = Coston2 RPC, `LOCAL_MODE=false`,
`SIMULATED_TEE=true`, `CHAIN_ID=114`, `NORMAL_PROXY_URL=https://tee-proxy-coston2-1.flare.rocks`),
and fill the `[db]` indexer credentials in `config/proxy/extension_proxy.coston2.docker.toml`
(copy from the `.example`).

### Deploy, start, register

```bash
./scripts/pre-build.sh
```

```bash
docker compose -f docker-compose.yaml -f docker-compose.coston2.yaml up -d --build
```

Confirm the **HL** extension booted (not Hello World) — you should see HL-config/treasury lines:

```bash
docker compose -f docker-compose.yaml -f docker-compose.coston2.yaml logs extension-tee | grep -iE 'hyperliquid|treasury|abstraction' | head
```

```bash
./scripts/post-build.sh
```

**Go/no-go #1 — attestation.** `post-build` runs `register-tee` (re-runnable `rRap`) with
`SIMULATED_TEE=true` (the documented Coston2 default). If it reverts on attestation/verification,
the Coston2 verifier may not accept the simulated code hash — obtain real enclave access, find a
testnet allowance with the Flare team, or run the whole thing locally (see the fallback below).
Confirm the extension id is live:

```bash
curl -s "$EXT_PROXY_URL/info" | grep -iE 'extension|codehash' | head
```

### Run the e2e with verification

```bash
cd tools && INSTRUCTION_SENDER=$(grep INSTRUCTION_SENDER ../config/extension.env | cut -d= -f2) && go run ./cmd/run-test -c "$CHAIN_URL" -p "$EXT_PROXY_URL" --instructionSender "$INSTRUCTION_SENDER" --treasury "<treasury-address>" --hlUrl "$HYPERLIQUID_URL"
```

**Go/no-go #2 — relay.** If the DEPOSIT step **times out** the result poll (rather than
returning a status), the live data providers didn't relay this extension to threshold weight —
use the fallback. A returned status (even an error) means relay works.

Expected success log: `deposit ok` → `derived HL account: 0x… balanceUsd=100.00` →
`verified perp position on HL: ETH szi=…` → `spot order accepted` → `withdraw ok` →
`verified ledger balance after withdraw: 80.00` → `All tests passed.`

Independently confirm the position on HL for the derived account printed above:

```bash
curl -s -X POST "$HYPERLIQUID_URL/info" -H 'content-type: application/json' -d '{"type":"clearinghouseState","user":"<derived-hl-address>"}' | head -c 800
```

### Fallback — local smoke-test

If either go/no-go fails, isolate the extension from Coston2 by running the same round-trip
locally, where `LOCAL_MODE=true` skips attestation and the sibling `e2e/` stack provides the
relay:

```bash
cd ../e2e/docker && docker compose up -d
```

```bash
./scripts/full-setup.sh --chain local --test
```

If the local round-trip passes but Coston2 doesn't, the failure is environmental (attestation
policy or data-provider relay coverage) — take the specific revert/timeout to the Flare team.
