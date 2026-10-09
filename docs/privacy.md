# Private deposits and withdrawals

Flare Private Perps keeps the original Hyperliquid-controlled-account model (see
[architecture.md](architecture.md)) and adds one thing: **unlinkable funding in
both directions**. This doc explains what that means, what it does and does not
hide, and how to configure it.

## The account model is unchanged (and already not public)

Each user still gets one Hyperliquid account whose key is derived inside the TEE:

```
Y = HKDF(masterSeed, flareAddress)      // masterSeed never leaves the enclave
```

Because the master seed lives only in the TEE, **an outsider cannot derive your HL
account from your Flare address.** The account model was never the leak. The leak is
*operational* - the moments money moves:

- **Deposit:** your on-chain deposit of amount A at time T, and your HL account Y
  getting funded with ~A at ~T, are trivially correlated by an observer.
- **Withdraw:** the mirror - Y unwinds ~B at T, your Flare address receives ~B at ~T.

Private deposit/withdraw break both correlations.

## The two pools (in the TEE, not on-chain)

```
DEPOSIT                                   WITHDRAW
  on-chain escrow -> Vault                  on-chain request (debits ledger now)
        |                                         |
   deposit pool (TEE)                        withdraw pool (TEE)
   randomized delay + batch                  randomized delay + batch
        |                                         |
   batch funder: treasury -> Y               payout batcher: Y -> treasury
   (fixed denomination, shuffled)            (fixed denomination, shuffled)
```

Both pools live **inside the enclave, in memory** - they hold, per user, the amount,
a randomized release time, and the recipe (which derived account to fund / settle).
They are deliberately **not on-chain**: anything on-chain is public and would re-link
everyone. The only on-chain artifact is the shared **Vault** escrow contract, which
just shows "someone deposited a denomination."

### What each mechanism buys

- **Randomized delay** (`HL_FUND_DELAY_MIN/MAX_SECONDS`) - decorrelates the funding
  moment from the deposit moment.
- **Batching** (`HL_BATCH_MIN`) - funds are moved for >= N users together, so the
  anonymity set is the batch size.
- **Fixed denominations** (`HL_DENOMINATIONS`) - uniform amounts, so an observer
  cannot re-link by matching a deposit amount to a funding amount.
- **Shared treasury as the funding origin** - money reaches Y from a common source,
  never from an address tied to you.

## What is and is not private (be honest)

- **Hyperliquid is a public ledger.** Anyone who learns an HL address sees all its
  positions and fills. Privacy here is **unlinkability** - nobody can prove account Y
  is yours - *not* trade secrecy.
- **Privacy strength = the anonymity set = batch size.** `HL_BATCH_MIN=1` provides
  zero cover. Real privacy needs a large batch and a long, randomized delay.
- **The enclave knows the mapping.** It must, to act for you. The guarantee is
  "cannot prove it to outsiders, cannot act without your instruction," not "nobody
  knows." That is the TEE-trust ceiling; on a Confidential Space VM
  (`SIMULATED_TEE=false`) even the operator cannot read enclave memory.
- **The final on-chain transfers are public by nature** - a deposit escrow into the
  Vault and a withdraw release from it are visible (the app surfaces both as explorer
  links). What stays hidden is *which HL account* they correspond to.

## Configuration

All env-configurable; off by default so the original synchronous behaviour is kept.

| Variable | Default | Meaning |
|---|---|---|
| `HL_PRIVATE_DEPOSIT` | `false` | Turn on delayed/batched private deposit **and** withdraw |
| `HL_DENOMINATIONS` | `25,100,500` | Allowed fixed deposit/withdraw amounts (USD) |
| `HL_BATCH_MIN` | `1` | Min matured entries funded/settled together (anonymity set) |
| `HL_FUND_DELAY_MIN_SECONDS` | `20` | Randomized delay lower bound |
| `HL_FUND_DELAY_MAX_SECONDS` | `60` | Randomized delay upper bound |
| `HL_FUND_TICK_SECONDS` | `5` | How often the batchers drain due entries |

**Demo defaults** (above) are short so the flow is visible. For a real deployment,
use a large batch and a long window, e.g.:

```
HL_PRIVATE_DEPOSIT=true
HL_DENOMINATIONS=25,100,500
HL_BATCH_MIN=10
HL_FUND_DELAY_MIN_SECONDS=1800     # 30 min
HL_FUND_DELAY_MAX_SECONDS=21600    # 6 h
```

## Correctness notes

- The background **reconciler skips users with an in-flight deposit or withdraw**, so
  it never credits/un-debits during the intentional delay window (which would collapse
  the delay and re-link by timing). The batchers are the sole authority for in-flight
  entries.
- A deposit is credited to the ledger **only after** the HL transfer confirms; a
  withdraw debits **at request** and reclaims at payout. A crash mid-batch leaves the
  entry pending and it retries - never double-counts.

## Code map

| Piece | File |
|---|---|
| Deposit pool (queue + timing) | `internal/extension/depositpool.go` |
| Batch funder (treasury -> Y) | `internal/extension/batchfunder.go` |
| Withdraw pool (reuses DepositPool) + payout batcher (Y -> treasury) | `internal/extension/withdrawbatcher.go` |
| Deposit handler branch | `internal/extension/processors.go` (`enqueuePrivateDeposit`) |
| Withdraw handler branch | `internal/extension/processors.go` (`enqueuePrivateWithdraw`) |
| Reconciler guard | `internal/extension/reconcile.go` |
| Config + denominations | `internal/config/config.go` |
| Pending-funding UX | `frontend/components/PooledDepositPanel.tsx`, `PooledWithdrawPanel.tsx` |
| On-chain activity (both directions) | `frontend/components/ActivityList.tsx` |
