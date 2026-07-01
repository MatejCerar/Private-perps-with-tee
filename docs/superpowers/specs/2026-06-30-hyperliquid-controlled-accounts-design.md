# Hyperliquid Controlled Accounts — Design (v1)

**Status:** Approved (initial draft) · **Date:** 2026-06-30 · **Repo:** `hype-accounts`

## 1. Goal & one-line model

A Flare Compute Extension where users trade on Hyperliquid (spot + perps) by sending
**plain on-chain instructions** on Flare. The TEE holds a **dedicated Hyperliquid account
per user**, executes their buy/sell/leverage actions on **Hyperliquid's real testnet**, and
the user never bridges funds off Flare.

UX target: AMM-simple — the user thinks in "trade" and "positions", not orderbooks. Limit
orders are supported under the hood, but the surfaced experience hides orderbook mechanics.

> **FSA deferred.** v1 identifies users by the EOA that calls the on-chain contract. Flare
> Smart Accounts are added in a later milestone. v1 uses normal Flare on-chain instructions,
> consistent with the other extensions and the scaffold.

## 2. Architecture & data flow

```
User EOA ──tx──► HyperliquidInstructionSender.sol (Flare / Coston2)
                   │ emits instruction: message = abi.encode(msg.sender, payload)
                   ▼
        Data providers relay + threshold-sign
                   ▼
              TEE proxy (queue)
                   ▼
   TEE machine ── extension.processAction(opCommand)
        │  1. recover user = decode(message).sender   (on-chain authenticated)
        │  2. derive user's HL key = HKDF(teeMasterSeed, user)
        │  3. build go-hyperliquid Exchange for that key
        │  4. fund / transfer / place order / cancel on HL testnet
        │  5. update internal ledger + cache
        ▼
   Signed ActionResult ──► proxy (clients poll result)
```

Long Hyperliquid REST calls use the existing **async pattern** (`handleAsync` returns
`pending`, a goroutine posts the real result once HL responds). This is inherited from the
prior `hyperliquid-extension` WIP and the scaffold.

## 3. Identity model

- The contract sets `message = abi.encode(msg.sender, <typed payload>)`. Because data
  providers relay exactly what the contract emitted on-chain, the embedded address is
  trustworthy and a user **cannot** act on behalf of another user.
- **No payload encryption in v1.** The prior WIP's `SET_KEY` / ECIES path is dropped
  entirely: the TEE owns all keys and no user secret ever enters the system.
- Identity unit for the per-user account and the ledger key is the Flare EOA address.

## 4. Per-user Hyperliquid account model

- **Derivation:** `userHLKey = HKDF(teeMasterSeed, "hl-account" || userFlareAddr)`.
  Deterministic, requires no per-user key storage, recoverable via the existing PMW / Shamir
  backup mechanism in a later milestone. The TEE *is* the account holder (it can both trade
  and `usdSend`), so no separate HL agent/API wallet is required.
- **Funding (mock deposit, real HL money):** one **treasury HL account** is seeded once from
  the testnet faucet (1,000 mock USDC). On a `DEPOSIT` instruction the TEE `UsdTransfer`s the
  credited amount from treasury → the user's derived HL account and records a ledger credit.
  No real FXRP moves in v1.
- **Spot vs perp wallets:** USDC arrives in the perp wallet; the TEE auto-runs
  `UsdClassTransfer` (perp ↔ spot) as needed so a spot order has spot USDC and a perp order
  has margin. Internal, invisible to the user.
- **Guardrails:** configurable per-deposit cap and global treasury cap. In v1 the deposit
  amount is asserted by the instruction and not yet backed by real FXRP (see Risks).

## 5. Instruction set

One OPType `HL`, sub-routed by OPCommand (matches the scaffold's documented OPType+OPCommand
pattern).

| OPCommand | Contract fn | Payload (after `sender`) | TEE action |
|---|---|---|---|
| `DEPOSIT` | `deposit(uint256 usdAmount)` | `usdAmount` | treasury → user `UsdTransfer`; ledger credit |
| `PLACE_ORDER` | `placeOrder(bytes)` | `{market: spot\|perp, coin, isBuy, orderType: market\|limit, size, limitPx?, reduceOnly?, leverage?, isCross?}` | set leverage if perp; class-transfer if needed; `MarketOpen` / `Order` |
| `CANCEL` | `cancel(bytes)` | `{coin, orderId}` | `BulkCancel` |
| `WITHDRAW` | `withdraw(uint256 usdAmount)` | `usdAmount` | close/settle as needed; user → treasury `UsdTransfer`; ledger debit (**stub — no FXRP back to Flare yet**) |

## 6. TEE extension internals

- State: `map[flareAddr] -> { hlAddress, ledgerBalance, lastKnownPositions, recentOrders }`,
  guarded by a mutex (extends the prior WIP's `Extension` struct).
- Per-action flow: decode → recover user → get-or-derive that user's `Exchange` client
  (cached) → execute → `buildResult`.
- Reuses the prior WIP's retrying HTTP client and async result plumbing.
- Library: `github.com/sonirico/go-hyperliquid` v0.33.1 (already vendored in the prior WIP).
  Confirmed methods: `Order`, `MarketOpen`, `MarketClose`, `BulkCancel`, `UpdateLeverage`,
  `UpdateIsolatedMargin`, `UsdTransfer` (usdSend), `UsdClassTransfer`, `ApproveBuilderFee`,
  `Info`.

## 7. Read path ("both")

- **Authoritative (TEE):** the TEE computes per-user `{hlAddress, ledgerBalance, positions}`
  (positions via `exchange.Info`) and surfaces it to clients. Exact surface — proxy state
  publication vs. a signed `QUERY` action — finalized in the implementation plan.
- **Live (frontend):** the TEE exposes each user's derived **HL address** (the only
  secret-derived value a client needs). A frontend reads positions/fills straight from HL's
  public info API for real-time UX.

## 8. Testing strategy

- **Unit:** identity decoding, key-derivation determinism, ledger math, payload validation
  (no chain / no HL).
- **Integration:** against HL **testnet** with a seeded treasury — deposit → place market and
  limit orders for both spot and perp → query positions → cancel → withdraw-stub round-trip.
- **E2E:** scaffold's `full-setup.sh` against local Flare infra and Coston2, sending real
  instructions through the proxy.

## 9. Out of scope / deferred

- Flare Smart Accounts (next milestone).
- Real FXRP escrow + FXRP→FUSD conversion; real withdrawal / settlement back to Flare.
- Multi-machine key consistency (see Risks) and Shamir backup of derived keys.
- Builder codes / fee monetization (library supports it; off in v1).

## 10. Risks & open questions

1. **Multi-machine routing.** Each TEE machine derives a *different* HL account for the same
   user, but the contract routes instructions to a random machine. **v1 = single TEE
   machine.** Multi-machine needs either sticky per-user routing or shared key custody via
   PMW — deferred, flagged now.
2. **Unbacked deposits in v1.** The deposit amount is trusted (mock), so a bad actor could
   try to drain the treasury → mitigated by per-deposit + global caps, testnet-only, small
   treasury.
3. **Treasury capacity.** Faucet gives ~1,000 mock USDC, one-time per address; this caps
   total demo capital. May seed multiple treasury addresses if needed.
4. **Fresh-account activation / `usdSend` minimums & fees** on testnet — verify a never-funded
   derived account can receive `UsdTransfer` and trade (expected yes) during implementation.
5. **Spot asset identifiers.** HL spot uses a different coin index format than perps; the
   order path needs a small asset-name → HL-identifier mapping.

## 11. Key facts referenced (Hyperliquid testnet)

- Sub-accounts are volume-gated (10 after $100k volume, up to 50) → ruled out as the per-user
  primary model.
- Agent/API wallets can sign but not withdraw; not needed because the TEE holds the account
  key directly.
- `usdSend` (`UsdTransfer`) moves USDC between any two HL accounts internally, no bridge —
  the funding primitive for the treasury model.
- Testnet faucet: 1,000 mock USDC, one-time per address, requires a $5 mainnet activation.
- Minimum order ≈ $10 notional; perps USDC-margined; spot trades against USDC.

## 12. Testnet integration findings (addendum, 2026-07-01)

Live testnet integration surfaced platform behavior that revised the funding model
(the per-user isolation model in §4 is unchanged and validated end-to-end):

- **Unified accounts disable inter-account transfers.** HL now defaults many accounts to a
  `unifiedAccount` abstraction mode where `usdSend`/`spotSend` are rejected
  (`"Action disabled when unified account is active"`). The treasury must be in **standard mode**
  (`abstraction: "disabled"`) to fund users. The TEE sets this via the `userSetAbstraction`
  action (not in go-hyperliquid; sent as a raw signed action), which is rejected while the
  account has open positions/orders.
- **Funding uses `spotSend`, not `usdSend`.** USDC lands in the recipient's spot balance;
  in standard mode spot/perp are separate, so the order handler sweeps collateral to the
  order's side (`spot→perp` for perp orders, `perp→spot` for spot buys) via `usdClassTransfer`.
- **go-hyperliquid v0.33.1 fragilities fixed in the adapter:** it panics building the spot-asset
  map on current testnet metadata (out-of-range token index) — the adapter pre-fetches and
  sanitizes `spotMeta`; and `UsdTransfer`/`SpotTransfer`/`UsdClassTransfer` do not inspect the
  response status, so a rejected transfer looked like success — the adapter surfaces the error.
- **Proven live:** treasury (standard) `spotSend` → fresh derived user account →
  `spot→perp` class transfer → perp order fills; position verified via HL's public info API.
- **Still deferred:** multi-user handler flow under real proxy/instruction load (on-chain e2e),
  the `$10` minimum-order handling in the UX, and everything in §9.
