# Status: Hyperliquid Funding Path — Validation Progress

> Last updated **2026-07-08**. This is the single "where does everything stand"
> document for the Flare → Arbitrum → Hyperliquid funding path (and reverse).
> The working checklist lives in
> `docs/superpowers/plans/2026-07-07-testnet-to-mainnet-plan.md` (gitignored);
> the on-chain e2e runbook is in `docs/hyperliquid.md` §11.

## TL;DR

Every component that **can** be proven without mainnet funds **has been proven
live**, most of them today:

| Component | Status | Proof |
|---|---|---|
| Bridge2 deposit / perp trade / withdraw3 (C1) | 🟢 live | `TestArbHLLoop`, `TestBridgeHandlersLive` |
| `batchedDepositWithPermit` (C2) | 🚫 blocked | not deployed on Bridge2 (probed testnet + mainnet) |
| Spot + leverage + cancel via handlers (C3) | 🟢 live | `TestBridgeHandlersLive` (limit + spot rest→cancel reflected in ledger) |
| Unified account mode (C4) | 🟢 live | `TestBridgeHandlersLive/unified`; set after first credit (HL rejects it pre-deposit — live finding) |
| Handlers + `/action` product surface (C5) | 🟢 live | bridge handlers driven directly; relay wrapper via the C8 e2e |
| Ledger + reconciliation (C6) | 🟢 live | `TestBridgeHandlersLive/reconcile` (converge + drift-heal) |
| Multi-user / concurrency (C7) | 🟢 live | `TestBridgeMultiUserLive` — 3 users parallel deposit+drain |
| On-chain instruction path (C8) | 🟢 live (**Coston**) | `run-test`: on-chain DEPOSIT→PERP→SPOT→WITHDRAW, verified on HL |
| PMW/Shamir custody (C9) | ❌ blocked | PMW not available yet |
| Arb One ↔ HL mainnet loop (C10) | ⏳ mainnet dust | same code as C1, mainnet config |
| Stargate bridge client (C11) | 🟢 client live | `TestStargateBridgeLive` — Arb Sepolia → Sepolia, exact quoted delivery |
| Swap client (C12) | 🟢 client (fork) | `TestSwapForkArbitrumOne` — quote-exact swaps, both router shapes |

**What remains before mainnet:** Phase 3 dust runs (C10 mainnet loop, real
Flare→Arb Stargate leg, real DEX swap leg, then full-path assembly), C9 when PMW
ships, and filling in the Flare SparkDEX router addresses. Testnet → mainnet is
a **config-only** swap; the profile is documented in `.env.example`.

## The architecture in one paragraph

Users hold funds on Flare; the extension custodies one **derived Hyperliquid
account per user** (seed-derived, TEE-held). Deposits travel Flare → (swap +
Stargate, C11/C12) → an **Arbitrum reserve** held by the TEE → the reserve
positions USDC + gas at the *user's own* Arbitrum address → a plain ERC20
transfer to **Bridge2** credits the user's HL account (deposits land in
perp/Core) → the account is switched to **unified** mode so spot and perp share
one collateral pool. Orders (perp/spot, market/limit, leverage, reduce-only,
cancel) run against the derived account; an in-memory **ledger** mirrors HL and
is **reconciled** to real HL equity in the background. Withdrawals go
`withdraw3` → the Arbitrum reserve (→ Stargate/swap back to Flare, Phase 3).
Instructions arrive on-chain (`InstructionSender` on Flare), get relayed by
data providers with threshold weight, and reach the extension through the TEE
proxy — the path C8 proved end-to-end.

## What was proven today (2026-07-08), with receipts

### 1. Consolidated Phase-1 live gate — `TestBridgeHandlersLive` ✅ (all 9 subtests)
`internal/extension/bridge_live_test.go` — Arb Sepolia + HL testnet:
deposit → **unified** (SetAbstraction accepted live) → perp market fill →
limit rest+cancel (`resting`→`cancelled` in the ledger) → **spot rest+cancel**
(sized via live spotMeta szDecimals — PURR needs whole-number sizes) →
withdraw3 → close → **reconcile** (ledger==real HL equity, then a simulated
late-credit drift healed) → **drain** (residual returned to the reserve so
runs stay balanced).

### 2. Multi-user live gate — `TestBridgeMultiUserLive` ✅
3 distinct derived users deposited **in parallel** through the real handler
(distinct HL accounts, exact per-user ledger credits, reserve nonce
serialization held), then drained in parallel to 0.50 dust each.
**Live finding:** HL rejects `userSetAbstraction` before an account's first
deposit ("Must deposit before performing actions") — `ensureUnified` now runs
**after** the first credit (`internal/extension/bridge.go`).

### 3. Stargate V2 bridge client — `internal/stargate` ✅ live
`QuoteSend` (quoteOFT + quoteSend) / `Send` (approve + taxi-mode) /
`WaitForBalance`. Live proof on Arb Sepolia → Ethereum Sepolia: quoted
4.996999 for 5.0 sent, **exact quoted amount delivered** (143s), self-funded
via the LZ testnet USDC's public mint. Unit tests pin the canonical IOFT
selectors. All four mainnet/testnet pool+token addresses verified on-chain via
`token()` and recorded in `.env.example` — the Flare OFT burns USDC.e and the
Arb One pool delivers **native USDC, the exact token Bridge2 takes**.

### 4. Swap client — `internal/swap` ✅ fork-proven
Uniswap V3-style exact-in with **both** router shapes (classic SwapRouter with
deadline — what SparkDEX-style forks use — and SwapRouter02) + QuoterV2 quotes.
Proof on an anvil fork of Arbitrum One (real bytecode/liquidity, zero real
funds): 50 USDT → 49.963509 USDC, **received == quote exactly** through both
routers.

### 5. On-chain instruction path (C8) — ✅ "All tests passed." on Coston
The full product path with the real Flare stack: `run-test` sent on-chain
instructions → live Coston data providers relayed (~12s each) → TEE proxy →
extension → HL testnet. DEPOSIT 100 → PERP 0.01 ETH (filled @1742.5, verified
on HL) → SPOT limit (rested, mid-relative pricing) → WITHDRAW 20 (ledger 80).
Independently confirmed on HL: derived account
`0x74E256B13AE5d992A2F02Cc39a4A3E47CFD163f1` shows the position + resting
order. Registered TEE `0x41081F9F…`, extension **65591**, `InstructionSender`
`0xc15110Fa…` on **Coston** (the operator env's chain; Coston2 is a config swap).

## Environment fixes that made C8 possible (read this before touching the stack)

The sibling `tee-node` / `tee-proxy` checkouts are detached at **May** commits;
this repo's modules (and the June-era `local/tee-proxy` image) expect the
**June** protocol (chain-id-bound signing). Symptoms fixed: `cmd/docker` did not
compile, and the ext-proxy panicked at boot with *"verifying response
signature: signature check fail"*.

Fix (no changes to the user's checkouts — **git worktrees**):

```
git -C ../../tee-node  worktree add ../tee-node-hype  31fc839ae6d2   # 2026-06-19
git -C ../../tee-proxy worktree add ../tee-proxy-hype develop        # 2026-06-23
```

- `go.mod` replace → `../../tee-node-hype` (June go-flare-common pin kept)
- `tools/go.mod` replaces → `../../../tee-node-hype`, `../../../tee-proxy-hype`
- `Dockerfile` COPYs `tee-node-hype/` into the image

The docker stack (redis + ext-proxy + extension-tee) is **left running** on the
Coston profile, registered and ready for repeat runs
(`./scripts/stop-services.sh` to stop).

## How to re-run every proof (one-liners)

```
# Unit tests (always safe)
GOCACHE=$TMPDIR/hype-gocache go test ./internal/...

# Phase-1 consolidated live gate + multi-user (HL testnet + Arb Sepolia; needs funded reserve)
HL_TREASURY_KEY=... go test -tags hltestnet ./internal/extension/ -run 'TestBridgeHandlersLive|TestBridgeMultiUserLive' -v -timeout 40m

# Stargate bridge client, live testnet pair (self-funding via public mint)
HL_TREASURY_KEY=... go test -tags hltestnet ./testing/ -run TestStargateBridgeLive -v -timeout 30m

# Swap client on an Arbitrum One fork (terminal 1: anvil --fork-url https://arb1.arbitrum.io/rpc)
go test -tags hltestnet ./testing/ -run TestSwapForkArbitrumOne -v

# On-chain e2e (stack must be up: ./scripts/start-services.sh --chain coston; then ./scripts/post-build.sh once)
cd tools && go run ./cmd/run-test -a ../config/coston/deployed-addresses.json -c "$CHAIN_URL" -p "$EXT_PROXY_URL" --instructionSender "$INSTRUCTION_SENDER" --treasury <treasury-hl-address> --hlUrl https://api.hyperliquid-testnet.xyz
```

(Go commands in this environment need the sandbox disabled and a writable
`GOCACHE`; never run `go mod tidy` / `go get`.)

## What's left (Phase 3 — mainnet, dust-first; all need operator sign-off + funds)

1. **C10:** Arb One ↔ HL mainnet loop with ~$5 native USDC (same code, mainnet profile in `.env.example`).
2. **C11-real:** Stargate Flare → Arbitrum One dust into the reserve (client proven; only pool addresses change).
3. **C12-real:** SparkDEX swap leg with dust (fill in Flare router/quoter addresses).
4. **Assembly:** full deposit path (Flare → HL) and withdraw path (HL → Flare) with dust, then the on-chain path on Flare mainnet, then cap ramp.
5. **C9 (blocked):** PMW/Shamir custody + multi-machine consistency, when PMW is available.
6. **C2 (blocked):** `batchedDepositWithPermit` — reopen if HL ever deploys it.

## Known caveats / notes

- The C8 e2e runs in **treasury funding mode** (the runbook's model); bridge-mode
  handlers are live-proven separately. A bridge-mode on-chain run needs
  `HL_FUNDING_MODE=bridge` + `ARB_*` env forwarded into the compose stack.
- The e2e leaves a small ETH perp position + a resting PURR order on the derived
  account `0x74E2…` (the driver has no close step; harmless on testnet).
- The throwaway live-test users keep ~0.5 USDC dust each by design (drain leaves
  a buffer under HL's min-withdraw constraints).
- `local/tee-proxy` docker image is from 2026-06-26 (June protocol). If it is
  ever rebuilt, build it from a tee-proxy commit that matches the tee-node
  worktree generation.
