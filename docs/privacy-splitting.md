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

**Layer 2 - split deposit + funding.** In `processDepositVault`, when splitting is
on: `chunks = splitIntoDenoms(usd, Denominations, maxShards, rnd)`; assign each
chunk a shard index; enqueue each `(user, shardIndex, denom)` into the deposit pool.
Requires the `DepositPool` to hold **multiple entries per user** (today it's one):
re-key its map by `(user, shardIndex)` or `map[user][]*pendingFunding`. The batch
funder funds each shard account from the treasury (existing `fundUserAccount`, but
to the shard address), credits the user ledger by the chunk.

**Layer 3 - aggregated read.** `stateHandler` / `fetchAccount` sum across the user's
shards: total balance = Σ shard balances; positions = merge same-coin positions
(sum size, notional-weighted entry, summed PnL/margin; recompute an aggregate
liquidation estimate). The FE already shows one balance/position, so no FE change if
`/state` returns the aggregate.

**Layer 4 - fan-out trading.** `processPlaceOrder` splits `size` across shards that
hold free margin, proportional to each shard's balance, placing a sub-order on each;
skip shards whose slice is below HL's ~$10 min order (route their share to larger
shards). `processCancel` cancels across shards. Aggregate the fills into one result.
Gotcha: min-order size caps how small a shard can trade - with 25-denom shards, a
coin needs size ≥ ~$10, so very small shards may sit idle for a given trade.

**Layer 5 - withdraw fan-in.** `processWithdraw` reclaims from shards back to the
treasury across a shuffled/delayed batch (mirror of funding), refunding the ledger
on expiry (existing self-heal applies).

## Known limits (unchanged)

Aggregate outliers and behavioral linkage still apply; decoy cover-traffic (treasury
funding dummy shards) is the complementary feature that makes low-volume unlinkable.
The enclave still knows the full user↔shards map (TEE-trust boundary).
