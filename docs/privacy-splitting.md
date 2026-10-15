# Deposit splitting across shard accounts (privacy v2)

Spread each deposit across several *separate standalone* HL accounts ("shards") so
no single account is an amount/aggregate fingerprint. Gated by `HL_SPLIT_DEPOSITS`
(default off); the single-account path is unchanged when off.

## Why this shape (settled)

- **Fixed denominations, not random amounts.** A unique total split into
  sum-preserving random chunks is reconstructable by subset-sum. Uniform
  denominations destroy uniqueness, so every chunk hides in a cohort. See
  [privacy.md](privacy.md).
- **Random count + sporadic timing, yes.** A deposit becomes a *random number* of
  denomination chunks funded at independent randomized delays.
- **Fan-out trading, not consolidation.** Orders are split across the user's shards
  so the shards never transfer to each other (which would re-link them).

## Built + unit-tested (layer 0)

- `keys.DeriveShardKey(seed, user, i)` - shard `i`'s key; shard 0 == `DeriveUserKey`
  (backward compatible). Deterministic.
- `extension.splitIntoDenoms(usd, denoms, maxShards, rnd)` - random denomination
  decomposition; verified: exact sum, every chunk a denomination, count ≤ maxShards,
  biased to spread. (`$500` → 8-14 shards.)

## Integration layers (each independently testable)

**Layer 1 - shard accounts.** `accountForShard(user, i)` using `DeriveShardKey`;
cache keyed by `(user, i)`. A per-user `shardCount` map (how many shards a user has,
grown as deposits split). Config: `HL_SPLIT_DEPOSITS`, `HL_MAX_SHARDS` (e.g. 15).

**Layer 2 - split deposit + funding (+ denomination ladder + remainder).**
- Denomination ladder widened to `HL_DENOMINATIONS=10,25,50,100,500` (min `$10`),
  so deposits decompose into many common small chunks and remainders are small.
- Allow **any** deposit amount ≥ `$10` (relax the "must equal a denomination"
  check). `chunks = splitIntoDenoms(usd, Denominations, maxShards, rnd)` funds the
  denominable part; the leftover `remainder = usd - Σchunks` (always `< $10`, e.g.
  `$37 → 25+10, remainder $2`) is **credited to the user ledger as un-deposited
  credit** - it never goes to HL, so no odd amount leaks. Display it: a line in the
  deposit modal before confirm ("$37 → $35 funded privately across N accounts · $2
  kept as credit") and a "+ $X credit" note under **Available** in the money panel.
- Assign each chunk a shard index; enqueue each `(user, shardIndex, denom)` into the
  deposit pool. Requires the `DepositPool` to hold **multiple entries per user**
  (today it's one): re-key its map by `(user, shardIndex)` or
  `map[user][]*pendingFunding`. The batch funder funds each shard account from the
  treasury (existing `fundUserAccount`, to the shard address) and credits the user
  ledger by the chunk.

**Layer 3 - aggregated read (BUILT + tested).** `reconcile` compares the ledger
against `aggregateHLTotalUSD` = Σ real equity across shards 0..depth, so split
funds on shards 1..N are no longer clawed back. `shardDepth` uses an in-memory
high-water mark and, on a cold cache (after an enclave restart), probes shards
1..MaxShards once to rediscover funded shards - so split balances survive a restart
with no persisted state.
Positions-merge (BUILT + tested): the enclave serves `GET /account?user=` (proxied
by the relayer at `GET /tee/account`) returning one aggregate view - summed
balance/withdrawable, concatenated open orders, and positions merged per coin
(summed size/PnL/margin/notional, size-weighted entry, notional-weighted liq). The
FE reads this one endpoint in split mode (`NEXT_PUBLIC_SPLIT_MODE=true`) instead of
querying HL per address, so it shows one balance + one position per coin and never
learns the shard addresses. CAVEAT: the read endpoint is currently unauthenticated
(fine behind the trusted local relayer for the demo); production must gate it to an
EIP-191 signature the enclave recovers to `user`, else it re-leaks the trading
activity the split hides.

**Layer 4 - fan-out trading (BUILT + tested).** `placeOrderFanOut` routes an order
across the user's funded shards: opening orders are sized proportional to each
shard's balance, reduce-only orders proportional to the position each shard holds.
Market orders fan out; limit orders stay on the largest shard so their id stays
cancellable. A sub-order HL rejects (e.g. a slice below the ~$10 min) has its size
re-placed on the largest shard in one fallback pass. `processCancel` cancels the id
across all shards best-effort. The degenerate case (only shard 0 funded) is
identical to the single-account path, and the whole path is gated on
`HL_SPLIT_DEPOSITS` so the proven single-account path is untouched when off.
Gotcha: min-order size still caps how small a shard can trade; tiny orders collapse
onto the largest shard via the fallback. Not yet live-tested against HL testnet.

**Layer 5 - withdraw fan-in (BUILT + tested).** In split mode `enqueueSplitWithdraw`
decomposes the amount into denomination chunks and packs one chunk per funded shard
(largest chunk to largest-balance shard), enqueuing each into the withdraw pool; the
payout batcher reclaims each shard->treasury across the shuffled/delayed batch
(mirror of funding), refunding the ledger on expiry (existing self-heal). Only the
packed total is debited; any amount that cannot be packed onto a shard this round is
left spendable and reported as the remainder (the request never hard-fails on
packing). `reclaimToTreasury` is shard-aware.

## Known limits (unchanged)

Aggregate outliers and behavioral linkage still apply; decoy cover-traffic (treasury
funding dummy shards) is the complementary feature that makes low-volume unlinkable.
The enclave still knows the full user↔shards map (TEE-trust boundary).
