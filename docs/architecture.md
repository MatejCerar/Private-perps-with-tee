# hype-accounts — Architecture &amp; Testing Guide

A deep, self-contained reference for **everything this extension is, how every part
works, and how it's tested.** For *where things currently stand* (proven / deferred /
blocked) see [`STATUS.md`](STATUS.md); for the mainnet rollout plan see
[`mainnet-runbook.md`](mainnet-runbook.md); for the HL-specific runbook see
[`hyperliquid.md`](hyperliquid.md).

## Contents
1. [What this is](#1-what-this-is)
2. [The big picture](#2-the-big-picture)
3. [The custody model & the byte-equality insight](#3-the-custody-model--the-byte-equality-insight)
4. [The Hyperliquid trading layer](#4-the-hyperliquid-trading-layer)
5. [The signing layer (hlsign)](#5-the-signing-layer-hlsign)
6. [The funding path](#6-the-funding-path)
7. [Reconciliation](#7-reconciliation)
8. [The Flare→Arbitrum leg (Stargate + swap)](#8-the-flarearbitrum-leg-stargate--swap)
9. [Key custody in depth (C9 / PMW)](#9-key-custody-in-depth-c9--pmw)
10. [The operator tools](#10-the-operator-tools)
11. [Configuration reference](#11-configuration-reference)
12. [Testing](#12-testing)
13. [Component status (C1–C12)](#13-component-status-c1c12)
14. [Design decisions & subtleties](#14-design-decisions--subtleties)

---

## 1. What this is

`hype-accounts` is a **Flare Confidential Compute (FCC) extension** — code that runs
inside a Trusted Execution Environment (TEE) behind Flare's TEE infrastructure — that
**custodies and controls one Hyperliquid (HL) trading account per user.** A user on
Flare issues instructions (deposit, place order, cancel, withdraw); the extension moves
real value onto/off of Hyperliquid and trades on the user's behalf, signing every action
with a key the user never touches.

Two axes define its behavior, each a config flag:

- **Key custody** (`HL_KEY_MODE`): **`seed`** (per-user keys derived in-process from a
  master seed) or **`tee-wallet`** (keys sealed inside the TEE, reached over a sign
  server — the "PMW" / private-managed-wallet model, tracked as **C9**).
- **Funding mode** (`HL_FUNDING_MODE`): **`treasury`** (deposits are HL `spotSend`s from
  a shared treasury) or **`bridge`** (deposits are real Arbitrum **Bridge2** deposits;
  withdrawals are `withdraw3`).

The headline design achievement is that **switching custody from `seed` to `tee-wallet`
changes only *where the private key lives*, not a single byte on the wire** — proven, not
assumed (§3).

---

## 2. The big picture

### Request flow

```
Flare user  ──on-chain instruction──▶  InstructionSender (Flare contract)
                                             │
                                    data providers relay + cosign (threshold weight)
                                             │
                                        TEE proxy (public)
                                             │  (main queue)
                                     extension (inside TEE)
                          ┌──────────────────┼───────────────────┐
                      accountFor(user)   ledger (in-mem)     signer (seed|TEE)
                          │
                     hl.Account  ──signed action──▶  Hyperliquid /exchange
```

Instructions arrive on-chain, are relayed+cosigned by Flare data providers, reach the
extension through the TEE proxy, and are processed asynchronously (each handler returns a
`pending` status immediately, then POSTs the final result back to the tee-node at
`/result`). This on-chain path is **C8**.

### The funding path (Flare → Arbitrum → Hyperliquid)

```
Flare user asset ─(SparkDEX swap)→ USDC.e ─(Stargate OFT)→ native USDC on Arb One
   → TEE Arbitrum reserve → (Bridge2 deposit) → HL credit
Withdraw is the reverse: HL withdraw3 → reserve → Stargate → SparkDEX → Flare user.
```

HL settles deposits/withdrawals on **Arbitrum** (Bridge2). The extension holds an
**Arbitrum reserve** that fronts each user's Bridge2 deposit. The Flare→Arbitrum front of
the pipeline (SparkDEX swap + Stargate bridge) has **proven client code but is not yet
wired into the production handler** — today the reserve is assumed already funded on
Arbitrum (§8).

### Deployment topology (what the tools operate against)

- **TEE node** (`tee-node`) — runs the extension + a **sign server** on `SIGN_PORT`
  (7701) exposing `POST /sign/{walletID}/{keyID}` and `GET /key-info/{walletID}/{keyID}`.
  Its **identity (`teeID`) is its enclave public key**, generated fresh each boot (no
  persistence) — so a restart can mint a new identity requiring re-registration.
- **TEE proxy** (`tee-proxy`) — public front; serves `/info`, `/wallet/*`, `/action/*`,
  relays instructions to the node's queues.
- **FlareTeeManager diamond** — one on-chain address exposing every TEE facet
  (`MachineManager`, `ExtensionManager`, `WalletManager`, `WalletKeyManager`,
  `WalletProjectManager`, `WalletBackupManager`, `MachinePathManager`, `Verification`,
  `OwnerAllowlist`, …). All facet bindings in `tools/pkg/support` point at this one
  address; the diamond routes each call to the right facet.

---

## 3. The custody model & the byte-equality insight

Everything in the extension signs through one abstraction (`internal/keys/signer.go`):

```go
type Signer interface {
    Address() common.Address
    SignPreimage(preimage []byte) ([]byte, error)   // -> 65-byte [R‖S‖V]
}
type Keyer interface { UserSigner(user common.Address) (Signer, error) }
```

**Why a *preimage*, not a digest?** The tee-node sign server *always applies keccak256
itself* — its algorithm is `crypto.Sign(keccak256(msg))`. So the interface standardizes on
"hand me the bytes that get hashed." If it took a digest, the TEE server would double-hash.
Because of this one decision, three key sources are interchangeable and **produce
byte-identical signatures**:

| Signer | Where the key lives | `SignPreimage` |
|---|---|---|
| `rawSigner` | in-process `*ecdsa.PrivateKey` | `crypto.Sign(keccak256(preimage), priv)` |
| `SeedKeyer`→`rawSigner` | derived from `HL_MASTER_SEED` (HKDF) | same, over the derived key |
| `teeWalletSigner` | **sealed in the TEE** | `POST /sign` → enclave signs `crypto.Sign(keccak256(preimage))` |

`rawSigner` is a *deliberate re-implementation* of the enclave's leaf algorithm so the
seed path and the TEE path emit the same bytes. Two canonical preimages:

- **EIP-712** (HL actions): `0x19 0x01 ‖ domainSeparator ‖ structHash` (`EIP712Preimage`).
- **Arbitrum legacy tx**: `RLP(nonce,gasPrice,gas,to,value,data,chainID,0,0)`.

**The correctness guard** (`SignLegacyTx`, `signer.go:96`): before signing an Arbitrum tx,
it reconstructs the EIP-155 RLP preimage and **asserts
`keccak256(preimage) == go-ethereum's types.Signer.Hash(tx)`** — a subtly-wrong preimage
fails loudly rather than silently recovering to the wrong address. Once this passes for the
seed path, the TEE path is *guaranteed* correct because it applies the identical
keccak256+sign to the identical bytes.

**Per-user key derivation** (`DeriveUserKey`, `derive.go:33`): HKDF-SHA256 over the master
seed with `info = "hl-account:" + lowercase(user.Hex()) + counter`, retrying the counter
until the 32 bytes are a valid secp256k1 scalar. Deterministic: same `(seed, user)` → same
key/address.

This byte-equality is **proven by tests**, not asserted (see §12): `rawSigner ==
crypto.Sign`, `teeWalletSigner == rawSigner` over a mock sign server, `SignLegacyTx ==
types.SignTx`, `EIP712Preimage == apitypes` digest, and `hlsign == go-hyperliquid`.

---

## 4. The Hyperliquid trading layer

`internal/hl/account.go` defines an `Account` interface and two factories:

```go
type Account interface {
    Address() string
    PlaceOrder(ctx, req OrderReq) (OrderResult, error)
    Cancel(ctx, coin string, oid int64) error
    SendUSD(ctx, dest string, amount float64) error       // spotSend
    Withdraw(ctx, dest string, amount float64) error      // withdraw3
    ClassTransfer(ctx, amount float64, toPerp bool) error // usdClassTransfer
    SpotUSDC(ctx) (float64, error)
    PerpWithdrawable(ctx) (float64, error)
    SetLeverage(ctx, coin string, leverage int, isCross bool) error
    SetAbstraction(ctx, mode string) error
    Positions(ctx) (PositionsSnapshot, error)
}
```

- **`realFactory`** (`NewRealFactory`, seed mode) — builds a `go-hyperliquid` `Exchange`
  **with the raw key** and wraps it in a `realAccount` whose `sx` (signer-exchange) is
  `nil`. State-changing calls dispatch to go-hyperliquid's own signing.
- **`teeWalletFactory`** (`NewTeeWalletFactory`, tee-wallet mode) —
  `AccountForSigner(signer)` builds a **read-only** `Exchange` using a fixed throwaway key
  (`readOnlyKeyHex = "111…1"`) **purely for `Info()` reads**, plus a `signerExchange` (`sx`)
  for all writes. The account is addressed by `signer.Address()`; the throwaway key never
  signs. So reads and the custody key are **decoupled**.

Every mutating method on `realAccount` is `if a.sx != nil { return a.sx.<x>(...) }` else
the go-hyperliquid path. Reads (`SpotUSDC`, `PerpWithdrawable`, `Positions`) always use
`a.ex.Info()` keyed on the address. `SetAbstraction` is special — it always goes through
`hlsign` (go-hyperliquid has no `userSetAbstraction` action).

**`signerExchange`** (`internal/hl/signerexchange.go`) is the tee-wallet write path. It
**reuses go-hyperliquid's exported wire types** (`OrderAction`, `OrderWire`,
`CancelAction`, …) and a read-only `Info` — only the signing leaf is swapped for `hlsign`.
Key points:
- **Strictly-increasing millisecond nonces** (`nextNonce`, mutex-guarded) — HL rejects
  non-increasing nonces.
- `placeOrder` builds a limit (`Tif:Gtc`) or market (aggressive IOC at `mid±5%`) order,
  formats prices/sizes via `floatToWire`, and POSTs `{action, nonce, signature}` to
  `/exchange`. Example wire body:
  ```json
  {"action":{"type":"order","orders":[{"a":<asset>,"b":true,"p":"3000","s":"0.5",
     "r":false,"t":{"limit":{"tif":"Gtc"}}}],"grouping":"na"},
   "nonce":<ms>,"signature":{"r":"0x…","s":"0x…","v":27}}
  ```
- `cancel`, `setLeverage`, `withdraw` (withdraw3), `sendUSD` (spotSend), `classTransfer`
  (usdClassTransfer) each build their action and post it.
- **`floatToWire`** reproduces go-hyperliquid's exact float formatting (`%.8f`, reject if
  precision loss ≥1e-12, trim zeros) so the signed msgpack matches byte-for-byte.

**`internal/ledger/ledger.go`** — the extension's authoritative *off-chain* view of each
user's USD balance + recent orders, keyed by the **Flare** address. `Credit`/`Debit`
(errors on overdraft)/`Adjust` (signed delta, clamps at 0 — for reconciliation's downward
corrections). It's a cache of truth, realigned to real HL equity by reconciliation (§7).

---

## 5. The signing layer (hlsign)

`internal/hl/hlsign/hlsign.go` reproduces Hyperliquid's two signing schemes but routes the
final signature through a `keys.Signer`. The msgpack/typed-data primitives are **vendored
verbatim from `go-hyperliquid@v0.33.1`** (pinned) so encoding can't drift; only the signing
leaf (`signInner`) is swapped.

**L1 actions** (order / cancel / updateLeverage) — `SignL1Action`:
`msgpack(action)` → `convertStr16ToStr8` (match Python's msgpack) → append nonce (8-byte BE)
+ vault flag → `keccak256` = **actionHash** → **phantom agent** `{source:"a"|"b",
connectionId:hash}` → EIP-712 typed data with domain `{Exchange, 1, chainId 1337, 0x0}` →
`signInner`. (ChainId `1337` is a synthetic signing-domain id, not a real chain.)

**User-signed actions** (withdraw3 / spotSend / usdClassTransfer / userSetAbstraction) —
`SignUserAction`: injects `signatureChainId=0x66eee` + `hyperliquidChain`, builds EIP-712
typed data with domain `{HyperliquidSignTransaction, 1, chainId 421614, 0x0}` and
`primaryType` (e.g. `HyperliquidTransaction:Withdraw`) → `signInner`.

Both converge in **`signInner`**:
```
domainSep  = HashStruct("EIP712Domain", domain)
structHash = hashStructLenient(typedData, primaryType, message)
sig        = signer.SignPreimage(EIP712Preimage(domainSep, structHash))  // 0x1901‖ds‖sh
return {R: sig[:32], S: sig[32:64], V: sig[64]+27}
```
`hashStructLenient` hashes **only the fields declared in `primaryType`**, ignoring extras
like `signatureChainId` — mirroring Python `eth_account`, which is what HL validates
against. The two schemes differ *only* in how `domainSeparator`/`structHash` are produced;
both end at one `SignPreimage` call — which is exactly why a TEE key can sign HL actions
identically.

---

## 6. The funding path

`internal/extension/` orchestrates instructions into value movement. The `Extension`
struct holds the seed factory, master seed, ledger, the custody keyer/signerFactory, the
funding-mode fields (treasury account **or** Arbitrum reserve), a per-user account cache, a
per-user mutex map (`userLocks`), and a `unifiedSet` (accounts switched to HL unified mode
once).

`accountFor(user)` is the single fork point: **tee-wallet** →
`signerFactory.AccountForSigner(keyer.UserSigner(user))`; **seed** →
`factory.Account(DeriveUserKey(seed, user), addr)`. Results are cached.

Instructions (`DEPOSIT`/`PLACE_ORDER`/`CANCEL`/`WITHDRAW`) are decoded from the on-chain
`DataFixed`, the inner payload is an ABI-`(address user, bytes jsonPayload)` envelope, and
each handler runs asynchronously (returns `pending`, then POSTs the result to the
tee-node's `/result`).

### The two funding modes

| | **treasury** (default) | **bridge** |
|---|---|---|
| Deposit | HL `spotSend` from a shared treasury | reserve → user's Arb addr → **Bridge2** → HL credit |
| Withdraw | HL `spotSend` back to treasury | HL **`withdraw3`** → the Arbitrum reserve |
| HL account mode | standard (spot/perp split; per-order class-transfer sweep) | **unified** (one pool; switched at first credit) |
| Min deposit | none (only caps) | `MinDepositUSD` (5.0) — sub-min deposits are lost |
| Reconciler | off | on |

> **Note:** in the production constructor `New`, the tee-wallet custody override *and* the
> background reconciler are wired only on the **bridge** branch. So tee-wallet custody and
> reconciliation are effectively bridge-mode features today.

### The reserve model (bridge mode)

The **reserve** is a single TEE-controlled Arbitrum identity (`reserveKey`/`reserveSigner`/
`reserveAddr`) holding native USDC + ETH gas, that **fronts** every user's Bridge2 deposit.
Because Bridge2 credits the *sending* address, the reserve cannot deposit on a user's
behalf directly — it must first move USDC (and gas) to the user's *own* Arbitrum address,
which then deposits. Withdrawals flow *back into* the reserve, making it a recycling float
hub. It also has its own HL account (`reserveHL`) to top up its Arbitrum USDC from HL via
`withdraw3` when auto-refill is on.

**Full DEPOSIT flow (`processDepositBridge`):**
1. Decode + validate (`≥ MinDepositUSD`, `≤ MaxDepositUSD`, total cap).
2. **Lock the per-user mutex** (serializes same-user ops + guards the ledger vs the
   reconciler).
3. Resolve `userSigner`/`userAddr`/`acct`.
4. **`ensureReserveFor(amount)` first** — before spending any gas, so a short reserve fails
   cleanly with no ETH stranded at the user.
5. Top up the user's Arbitrum ETH gas if below threshold (from the reserve).
6. `TransferUSDC(reserve → userAddr)` and wait for it to land (Bridge2 credits the sender).
7. Snapshot HL equity → `DepositToBridge(userSigner, …)` (a Bridge2 deposit is just an
   ERC20 `transfer` to the bridge, signed by the **user's** signer so HL credits the user)
   → **`waitForHLCredit`** (polls up to `hlCreditTimeout` = 4 min). On timeout: fail
   loudly and **neither credit nor debit** — a late credit is later absorbed by
   reconciliation, avoiding double-credit on retry.
8. `ensureUnified` (once, after first credit) — HL rejects `SetAbstraction` on a
   never-deposited account, so it must run *after* the first credit.
9. `ledger.Credit(user, amount)`.

**Full WITHDRAW flow (`processWithdrawBridge`):** lock user mutex → `ledger.Debit` first
(errors on insufficient) → `acct.Withdraw(reserveAddr, amount)` (HL `withdraw3` delivers
native USDC to the reserve in ~3–4 min, **minus a flat ~1 USDC fee**) → refund the ledger
on failure.

**Concurrency:** two layers — the **outer `userMutex`** serializes same-user op chains
(and is TryLock-skipped by the reconciler), and the **inner `sendMu`** in `arb.Client`
serializes all shared-reserve-key Arbitrum txs across the read-nonce→send→wait sequence
(nonce safety).

`internal/arb/client.go` is the Arbitrum client (`TransferUSDC`, `SendETH`,
`DepositToBridge` = a plain ERC20 transfer to the bridge, `WaitForUSDCBalance`). `sendTx`
holds `sendMu`, doubles the suggested gas price and pads the gas estimate +25% (both
surplus refunded on Arbitrum), and signs via `keys.SignLegacyTx(tx, chainID, from)` — so
the reserve *or* a user *or* a TEE key can send Arbitrum txs through the same path.

`internal/arb/permit.go` implements `batchedDepositWithPermit` (EIP-2612 gasless deposits)
— **shelved (C2): the contract isn't deployed on Bridge2**, and skipped entries make a
mined receipt non-authoritative. The production path uses the per-user reserve-fronted
flow instead.

---

## 7. Reconciliation

The ledger drifts from real HL equity from (a) late deposit credits (a deposit that timed
out in `waitForHLCredit` but credited afterward), (b) trading P&amp;L, and (c) the ~1 USDC
`withdraw3` fee. `internal/extension/reconcile.go` realigns it.

`Reconcile(user)`: `TryLock` the user mutex — **skip if busy** (an in-flight op owns it;
the next tick catches it — this is the race-safety core). Compute `delta = realHLEquity -
ledgerBalance`; if `|delta| ≤ ReconcileToleranceUSD` (0.01) leave it; positive →
`Credit`, negative → `Adjust` (clamps at 0). `ReconcileAll` snapshots the user set and
reconciles each. `startReconciler` ticks every `ReconcileIntervalSeconds` (default 120s),
started only by the production `New` in bridge mode. `hlTotalUSD` = perp account value +
spot USDC, robust to whether a bridge deposit is reported on the perp or spot side.

---

## 8. The Flare→Arbitrum leg (Stargate + swap)

Two client packages implement the front of the pipeline. **Both are proven in isolation but
not yet imported by any non-test code** — today's production deposit assumes the reserve is
already funded on Arbitrum, and `refillReserve` tops it up from the reserve's *HL* balance
(withdraw3), not from Flare.

- **`internal/stargate/client.go`** — a Stargate V2 (LayerZero OFT) client. One `IOFT`
  surface covers both the Arbitrum native-USDC pool and the Flare `StargateOFTUSDC` (which
  burns USDC.e). `QuoteSend` (two-step `quoteOFT` + `quoteSend` so the returned fee matches
  the exact `Send`), `Send` (approve + **taxi mode** — empty `oftCmd` = immediate single
  message), `WaitForBalance` (observe the async cross-chain delivery). Testnet↔mainnet is
  config-only; mainnet delivers **native USDC — exactly the token Bridge2 takes**.
- **`internal/swap/client.go`** — a Uniswap-V3-style exact-in swap client supporting
  **both router shapes**: `RouterClassic` (original `SwapRouter`, `exactInputSingle`
  *with* deadline — the **SparkDEX** shape) and `Router02` (no deadline). `QuoteExactIn`
  (QuoterV2) derives `minOut`; `SwapExactIn` approves + swaps. SparkDEX's verified mainnet
  addresses (classic router, QuoterV2, WFLR) are recorded in `.env.example` and
  [`STATUS.md`](STATUS.md).

Conceptually: Flare asset → (SparkDEX swap) → USDC.e → (Stargate OFT) → native USDC in the
Arbitrum reserve → (Bridge2) → HL. Withdraw reverses it.

---

## 9. Key custody in depth (C9 / PMW)

In **tee-wallet** mode the extension never holds a user's private key. Instead:

- Each user maps to a provisioned **(walletID, keyID)** in a registry
  (`internal/keys/registry.go`, a JSON file written by the provisioning tool).
- `TeeWalletKeyer.UserSigner(user)` looks up the pair, resolves the address once from
  `GET /key-info/{walletID}/{keyID}` (a 64-byte X‖Y pubkey → `keccak256(X‖Y)[12:]`), and
  returns a `teeWalletSigner`.
- `teeWalletSigner.SignPreimage` does `POST /sign/{walletID}/{keyID}` with
  `{"message":"<base64 preimage>"}`; the enclave applies keccak256, signs, and returns
  `{"signature":"<base64 65-byte [R‖S‖V]>"}`.

**Provisioning** (`tools/cmd/provision-wallet`) makes an enclave-sealed key actually exist,
via the on-chain wallet lifecycle against the diamond (each step gated by a letter of
`-command`, default `pwactkfe`):

1. **CreateProject**(extensionID, EVM key type, EVM sign algo) → projectID
2. **CreateWallet**(projectID) → walletID
3. **SetAdmins** + each admin **ConfirmAdmin** (admins are separate EOAs; the tool
   gas-funds them first, default 0.5 native each) — a **k-of-n admin *restore* threshold**
   (default 2-of-3)
4. **CloseWalletInitialization** (freeze config)
5. **SetMultisigThreshold(1)** — the **cosigner** threshold (how many TEEs co-sign; 1 for
   a single-TEE wallet — distinct from the admin threshold above)
6. **AddKey**(teeID, walletID) → keyID (the enclave generates the key; instruction payment
   required)
7. **KeyProof** (poll the proxy `/wallet/{walletID}/{keyID}` for the enclave's 65-byte
   existence proof) → **ConfirmKey** on-chain
8. **EnableWallet**

Then it derives the HL address from the on-chain public key and writes the registry entry.

**Multi-machine** (replicate a key A→B so both nodes derive the same HL address): a second
TEE node (**node B**) was stood up and registered to production on Coston, but the actual
replication tooling (machine-path governance + `DirectBackup`/`DirectRestore`) is
**deferred** — see the multi-machine memory note and [`STATUS.md`](STATUS.md).

---

## 10. The operator tools

Under `tools/` (module `extension-scaffold/tools`):

- **`tools/pkg/support`** — `Support` struct with every TEE facet binding wired to the one
  `FlareTeeManager` diamond, plus `Prv` (deployer key from `DEPLOYMENT_PRIVATE_KEY`),
  `ChainID`, `ChainClient`, and `CheckTx` (waits + decodes revert reasons).
- **`tools/pkg/fccutils`** — the on-chain call wrappers: `wallet.go` (the wallet-lifecycle
  txs + `GetProxyWalletKeyInfo`/`KeyProof`), `registration.go` (the resumable TEE-node
  registration state machine), revert decoding.
- **`tools/cmd/provision-wallet`** — the 8-step wallet provisioning above.
- **`tools/cmd/register-tee`** — registers a running node's machine on-chain and drives it
  to production: `r` PreRegistration (`MachineManager.Register`), `R` fresh attestation,
  `a` FDC availability check, `p` `ToProduction`. (Requires the node's proxy publicly
  reachable so Flare's FDC/data-providers can attest it.)
- **`tools/cmd/run-test`** — the on-chain e2e driver (C8): sends DEPOSIT→PERP→SPOT→WITHDRAW
  instructions through the real stack and verifies each against HL. Flags include
  `-depositUsd`/`-withdrawUsd` (added for bridge-mode dust runs) and a widened result-poll
  window (a bridge DEPOSIT can block up to 4 min).
- **`docker-compose*.yaml`** — the base stack (`redis`, `ext-proxy`, `extension-tee`) with
  Coston/Coston2 overlays; `docker-compose.bridge.yaml` overlays bridge-mode env;
  `docker-compose.nodeb.yaml` is the second-node stack.

---

## 11. Configuration reference

Loaded in `internal/config/config.go` (`LoadHLConfig` enforces the required secrets;
`LoadArbConfig` is all-optional and also reads the key-custody vars).

| Env var | Default | Meaning |
|---|---|---|
| `HYPERLIQUID_URL` | testnet API | HL API base (selects the signing domain) |
| `HL_MASTER_SEED` | — (**required**) | ≥32-byte seed for per-user key derivation |
| `HL_TREASURY_KEY` | — (**required**) | treasury HL key; also the default Arb reserve key |
| `HL_MAX_DEPOSIT_USD` / `HL_MAX_TOTAL_DEPOSIT_USD` | 1000 / 5000 | deposit caps |
| `HL_FUNDING_MODE` | `treasury` | `treasury` \| `bridge` |
| `HL_KEY_MODE` | `seed` | `seed` \| `tee-wallet` |
| `HL_SIGN_URL` | `http://127.0.0.1:7701` | tee-node sign server (`/sign`, `/key-info`) |
| `HL_WALLET_REGISTRY` | `./config/wallet-registry.json` | user→(walletID,keyID) map |
| `ARBITRUM_RPC_URL` | Arb Sepolia | Arbitrum RPC |
| `ARB_USDC_ADDRESS` / `HL_BRIDGE2_ADDRESS` | HL-testnet defaults | bridge token + Bridge2 |
| `ARB_RESERVE_KEY` | falls back to `HL_TREASURY_KEY` | TEE Arbitrum reserve key |
| `HL_MIN_DEPOSIT_USD` | 5.0 | Bridge2 minimum (sub-min deposits are lost) |
| `HL_RESERVE_AUTO_REFILL` / `HL_RESERVE_REFILL_FLOOR_USD` | false / 50 | reserve top-up from HL |
| `HL_RECONCILE_TOLERANCE_USD` / `HL_RECONCILE_INTERVAL_SECONDS` | 0.01 / 120 | reconciler (0 = off) |
| `EXTENSION_PORT` / `SIGN_PORT` / `TYPES_SERVER_PORT` | 8080 / 9090 / 8100 | ports |

Tooling: `DEPLOYMENT_PRIVATE_KEY` (deployer/owner; falls back to a Hardhat dev key if
unset — a footgun on real networks), `HL_WALLET_ADMIN_KEYS` (the 2-of-3 admin keys for
provisioning). The `.env` mainnet profile (verified addresses) is staged in `.env.example`.

---

## 12. Testing

Five categories. **🟢** offline/CI-safe · **🟡** needs a local node/fork, zero real funds ·
**🔴** needs funded keys + live network. Two build tags gate the non-offline tests:
`hltestnet` (live + fork) and `integration` (on-chain contract tests). Live tests
`t.Skip` cleanly when their env var is absent, so tagged runs still compile offline.

### Unit (🟢, no tag) — the proof layer

- **Signing byte-equality** (the crown jewels): `internal/keys/signer_test.go` (`rawSigner
  == crypto.Sign`; `SignLegacyTx == types.SignTx`; `EIP712Preimage == apitypes` digest),
  `internal/keys/teewallet_test.go` (`teeWalletSigner == rawSigner` over an `httptest`
  mock of the enclave sign server — **the core custody proof**),
  `internal/hl/hlsign/hlsign_test.go` (`hlsign.SignL1Action`/`SignUserAction` == upstream
  `go-hyperliquid`, both nets), `internal/hl/signerexchange_test.go` (`floatToWire`
  vectors + order-action signing), `internal/keys/derive_test.go`.
- **Ledger:** `internal/ledger/ledger_test.go` (credit/debit/overdraft/cancel).
- **Handlers vs fakes** (`fakeArb` + `hl.FakeAccount`):
  `internal/extension/processors_test.go` (treasury deposit/order-with-sweep/cancel/
  withdraw), `internal/extension/bridge_test.go` (reserve-funded deposits, no-gas-stranded
  on failure, credit-timeout leaves ledger un-credited, unified-once, no-sweep in unified,
  **20-way concurrent deposits**), `internal/extension/reconcile_test.go` (late credit /
  P&amp;L / fee / clamp / tolerance / skip-busy / all).
- **ABI-selector pins** (catch tuple/field drift offline): `internal/stargate/client_test.go`,
  `internal/swap/client_test.go`.
- **Envelope / config:** `pkg/types/types_test.go`, `internal/config/config_test.go`.
- **Tooling** (deploy-hardening): `tools/pkg/{fccutils,support,validate,hlinfo}` unit tests
  (revert decoding, registration state I/O, verify-deploy checks, HL `/info` client).

### Live (🔴, `hltestnet`)

- **`TestBridgeHandlersLive`** (`internal/extension/bridge_live_test.go`) — the
  consolidated Phase-1 gate: 9 ordered subtests (deposit → unified → perp → limit+cancel →
  spot+cancel → withdraw → close → **reconcile** → drain) over Arb Sepolia + HL testnet.
- **`TestBridgeMultiUserLive`** — 3 users deposit+drain in parallel (reserve nonce
  serialization + per-user isolation). **C7.**
- **`TestTeeWalletOrderLive`** (`internal/hl/teewallet_live_test.go`) — **the C9 e2e**: a
  TEE-sealed key (test holds no private key) signs `SetAbstraction` + a resting limit
  order + cancel, all accepted by HL. Reaches the in-container sign server via a `socat`
  sidecar.
- **`TestArbHLLoop`** (`testing/arb_loop_test.go`) — the whole Arb Sepolia↔HL loop, C1.
- **`TestStargateBridgeLive`** — C11, exact quoted delivery on a real testnet OFT pair.
- Various treasury-mode / adapter / diagnostic tests in `testing/`.

### Fork (🟡, `hltestnet`) — `TestSwapForkArbitrumOne`

Proves the swap client on an **anvil fork of Arbitrum One** (real V3 bytecode + liquidity,
zero real funds; self-funds via `anvil_*` cheatcodes), both router shapes, `received ==
quote`. **C12.** Start `anvil --fork-url https://arb1.arbitrum.io/rpc` first.

### Integration (🟡, `integration`) — `tools/integration/`

On-chain tests of the deploy path (constructor validation, `setExtensionId` errors,
`CheckTx` revert decoding, idempotent registration, pre-flight checks) against a local
Hardhat/Anvil or Coston2.

### On-chain e2e (🔴) — `tools/cmd/run-test`

Drives DEPOSIT→PERP→SPOT→WITHDRAW through the real Flare stack (on-chain InstructionSender →
data-provider relay → TEE proxy → extension → HL). **C8.**

### Run mechanics (this repo)

Go here needs the **sandbox disabled** and `GOCACHE=$TMPDIR/hype-gocache` (the sandbox
blocks the build cache and macOS TLS). **Never** `go mod tidy` / `go get` — the module
`replace`s point at pinned June-era `tee-node`/`tee-proxy` worktrees.

```bash
# UNIT (offline)
GOCACHE=$TMPDIR/hype-gocache go test ./internal/...        # + ./pkg/... ; and (cd tools && go test ./...)
# LIVE Phase-1 gate + multi-user
HL_TREASURY_KEY=... go test -tags hltestnet ./internal/extension/ -run 'TestBridgeHandlersLive|TestBridgeMultiUserLive' -v -timeout 40m
# LIVE tee-wallet C9 (enclave sign server published at HL_SIGN_URL + provisioned registry)
HL_TREASURY_KEY=... HL_SIGN_URL=http://127.0.0.1:7701 HL_WALLET_REGISTRY=config/wallet-registry.json go test -tags hltestnet ./internal/hl/ -run TestTeeWalletOrderLive -v
# FORK swap (terminal 1: anvil --fork-url https://arb1.arbitrum.io/rpc)
go test -tags hltestnet ./testing/ -run TestSwapForkArbitrumOne -v
# INTEGRATION (tools module, live EVM node)
cd tools && go test -tags integration ./integration/ -v -count=1
# ON-CHAIN E2E (stack up via ./scripts/start-services.sh --chain coston; ./scripts/post-build.sh once)
cd tools && go run ./cmd/run-test -a ../config/coston/deployed-addresses.json -c "$CHAIN_URL" -p "$EXT_PROXY_URL" --instructionSender "$INSTRUCTION_SENDER" --hlUrl https://api.hyperliquid-testnet.xyz
```

---

## 13. Component status (C1–C12)

Full detail + receipts in [`STATUS.md`](STATUS.md). Summary:

| C | Component | Status | Proven by |
|---|---|---|---|
| C1 | Bridge2 deposit / perp / withdraw3 | 🟢 live | `TestArbHLLoop`, `TestBridgeHandlersLive` |
| C2 | `batchedDepositWithPermit` | 🚫 blocked | not deployed on Bridge2 |
| C3 | Spot + leverage + cancel | 🟢 live | `TestBridgeHandlersLive` |
| C4 | Unified account mode | 🟢 live | `TestBridgeHandlersLive/unified`, `TestTeeWalletOrderLive` |
| C5 | Handlers + `/action` surface | 🟢 live | handlers + the C8 e2e |
| C6 | Ledger + reconciliation | 🟢 live | `reconcile_test.go`, `TestBridgeHandlersLive/reconcile` |
| C7 | Multi-user / concurrency | 🟢 live | `TestBridgeMultiUserLive` |
| C8 | On-chain instruction path | 🟢 live (Coston, treasury) | `run-test` |
| C9 | PMW / tee-wallet custody | 🟢 single-node closed; multi-machine deferred | `TestTeeWalletOrderLive` |
| C10 | Arb One ↔ HL mainnet loop | ⏳ mainnet dust | same code as C1 |
| C11 | Stargate bridge client | 🟢 client live | `TestStargateBridgeLive` |
| C12 | Swap client | 🟢 client (fork) | `TestSwapForkArbitrumOne` |

**Known open items:** C9 multi-machine replication (tooling unbuilt); bridge-mode **on-chain**
wiring (blocked at the data-provider relay to a freshly re-registered node — the deposit
handlers themselves are proven); the Flare→Arb legs (Stargate/swap) not yet wired into the
production handler; and mainnet (needs a Flare-mainnet deployment + real funds).

---

## 14. Design decisions & subtleties

1. **Preimage-not-digest** is the linchpin: the enclave re-hashes, so the `Signer` takes
   pre-hash bytes — making raw/seed/TEE keys interchangeable and byte-identical. Proven,
   not assumed (§3, §12).
2. **`SignLegacyTx` verifies the preimage** against go-ethereum's own tx hash before
   signing — a wrong preimage fails loudly.
3. **Vendored, pinned go-hyperliquid primitives** (`@v0.33.1`): `convertStr16ToStr8` +
   unsorted msgpack keys + compact ints make Go's msgpack match Python's; any drift
   silently breaks signatures — hence the byte-equality tests.
4. **`sanitizedSpotMeta` is load-bearing** — without it, `NewExchange`'s own testnet
   spotMeta fetch indexes past the tokens slice and panics.
5. **`readOnlyKeyHex` decouples reads from custody** — a throwaway key builds the
   read-only `Exchange`; writes go through the real (TEE) signer.
6. **Deposits land in perp/Core; unified-after-first-credit** — HL rejects `SetAbstraction`
   on a never-deposited account, so it runs after the first credit (a live finding).
7. **The ~1 USDC withdraw3 fee** and **late deposit credits** are absorbed by
   reconciliation, not by the handlers — which is why the handler leaves the ledger
   untouched on a credit timeout (no double-credit on retry).
8. **Sub-min deposits (&lt;5 USD) are rejected upstream** — HL won't credit them and the
   funds are unrecoverable.
9. **Two-layer concurrency:** per-user mutex (serialize op chains, TryLock-skipped by the
   reconciler) + reserve `sendMu` (serialize shared-key Arbitrum nonces).
10. **Ephemeral node identity:** a TEE node's `teeID` is its enclave pubkey, regenerated on
    some restarts (observed to change when the proxy is recreated, stable on a node-only
    restart) — a change orphans the on-chain registration and in-enclave keys and needs
    re-registration.
11. **`HL_KEY_MODE`/`funding` wiring caveat:** tee-wallet custody + the background
    reconciler are activated only on the bridge branch of the production `New`.
12. **Stargate/swap clients are proven but not yet integrated** into the production funding
    handler.

---

*Generated from a full read of the codebase (`internal/`, `pkg/`, `tools/`, tests) on
2026-07-09. For the live status and receipts, always cross-check [`STATUS.md`](STATUS.md).*
