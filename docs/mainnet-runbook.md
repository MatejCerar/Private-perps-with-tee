# Mainnet Runbook — Flare → Arbitrum → Hyperliquid funding path (dust-first)

> Planning doc, no spend. Every component that can be proven without mainnet
> funds is already green on testnet/Coston (see `docs/STATUS.md`). This is the
> ordered, dust-first sequence to bring the path live on mainnet, what funds/keys
> the operator must provide, and the verification + rollback at each step.
>
> **Guiding principle:** prove each leg in isolation with ~$5 dust, then assemble,
> then ramp caps. Never send more than the current step has proven it can recover.

## The path (one line)

Deposit: user on **Flare** → **SparkDEX** swap (WFLR→USDC.e) → **Stargate** OFT
(Flare→Arb One, delivers native USDC) → **Arbitrum reserve** → **Bridge2** deposit
→ **HL** credit. Withdraw is the exact reverse (HL `withdraw3` → reserve →
Stargate Arb→Flare → SparkDEX swap → user).

## Mainnet addresses (all verified on-chain; see `.env.example` + memory)

| Leg | Contract | Address |
|---|---|---|
| HL | API | `https://api.hyperliquid.xyz` |
| HL | Bridge2 (Arb One) | `0x2Df1c51E09aECF9cacB7bc98cB1742757f163dF7` |
| Arb One | native USDC | `0xaf88d065e77c8cC2239327C5EDb3A432268e5831` |
| Stargate | Flare OFT (USDC.e, eid 30295) | `0x77C71633C34C3784ede189d74223122422492a0f` |
| Stargate | Arb One pool (native USDC, eid 30110) | `0xe8CDF27AcD73a434D661C84887215F7598e7d0d3` |
| Flare | USDC.e (Stargate OFT token) | `0xFbDa5F676cB37624f28265A144A48B0d6e87d3b6` |
| SparkDEX | SwapRouter (classic, deadline) | `0x8a1E35F5c98C4E85B36B7B253222eE17773b2781` |
| SparkDEX | QuoterV2 | `0x5B5513c55fd06e2658010c121c37b07fC8e8B705` |
| Flare | WFLR (wrapped native) | `0x1D80c49BbBCd1C0911346656B529DF9E5c2F783d` |

Config swap: replace the testnet block in `.env` with the MAINNET profile block
already staged in `.env.example` (chain URLs, USDC, Bridge2, Stargate). Testnet →
mainnet is **config-only** for the client legs.

## Preconditions the operator provides

1. **Arbitrum reserve** (`ARB_RESERVE_KEY`), funded on **Arb One**: ~$20 native
   USDC + ~$5 ETH gas. This is the TEE-held reserve that fronts Bridge2 deposits.
2. **Flare funding source**, funded on **Flare mainnet**: ~$20 in WFLR (or FLR to
   wrap) + FLR gas, to prove the swap + Stargate legs.
3. **HL mainnet**: the reserve's HL account reachable (it is auto-created by the
   first Bridge2 deposit; no pre-funding needed).
4. **SparkDEX router/quoter fee tier**: confirm the WFLR/USDC.e pool exists and
   pick its fee tier (call `QuoterV2.quoteExactInputSingle` — step C12 does this).
5. **(For the on-chain instruction path only — steps 5+):** the FCC diamond +
   this extension deployed **and registered on Flare mainnet**, with a registered
   TEE machine reachable at a public URL, and Flare mainnet data providers relaying.
   The client legs (C10–C12, assembly) do **not** need this — they run from the
   reserve key directly.

## Ordered sequence

### C10 — Arb One ↔ HL mainnet loop (~$5) — *same code as the proven C1*
Reserve deposits $5 native USDC to Bridge2 → confirm HL credit → tiny perp
order + cancel → `withdraw3` $4 back to the reserve. Proves the mainnet Bridge2 +
HL leg end-to-end.
- Run: the existing `internal/arb` + `internal/hl` bridge path with the mainnet
  profile; drive via `internal/extension` bridge handlers (as `TestBridgeHandlersLive`
  does, pointed at mainnet).
- Verify: HL shows the deposit credited to the reserve's HL account; withdraw3
  returns to the reserve's Arb One address (minus HL's ~$1 fee).
- Rollback: withdraw3 everything back; funds never leave the reserve's control.

### C11-real — Stargate Flare → Arb One (~$5) — *client proven live on testnet*
Send $5 USDC.e from Flare via the Stargate OFT; confirm the Arb One pool delivers
native USDC to the reserve.
- Run: `internal/stargate` `QuoteSend` → `Send` (Flare OFT `0x77C7…` → dest eid
  30110, `to` = reserve Arb One address), then `WaitForBalance` on native USDC.
- Verify: reserve's Arb One native-USDC balance rises by the quoted amount.
- Note: the OFT burns USDC.e on Flare and the Arb One pool delivers **native
  USDC — the exact token Bridge2 takes** (verified). No extra hop needed.

### C12-real — SparkDEX swap WFLR → USDC.e (~$5) — *client fork-proven on Arb One*
Swap ~$5 WFLR → USDC.e on SparkDEX to produce the token Stargate bridges.
- Run: `internal/swap` classic-SwapRouter path against `0x8a1E…`; quote first via
  `QuoterV2 0x5B55…`. Confirm the WFLR/USDC.e pool + fee tier.
- Verify: received USDC.e ≈ quote; slippage within tolerance.
- Rollback: swap back USDC.e → WFLR if needed (both directions supported).

### Assembly — full deposit + withdraw with dust (~$5)
Chain the legs on mainnet, smallest amount that clears every minimum (HL min
deposit $5; SparkDEX/Stargate dust):
1. Deposit: Flare user WFLR → (C12 swap) → USDC.e → (C11 Stargate) → reserve Arb
   One native USDC → (C10 Bridge2) → HL credit.
2. Withdraw: HL `withdraw3` → reserve → (Stargate Arb→Flare) → USDC.e →
   (SparkDEX swap) → WFLR → Flare user.
- Verify each hop's balance delta before triggering the next; abort on any
  shortfall (the reserve retains custody throughout).

### On-chain instruction path on Flare mainnet — *the C8 path, mainnet*
Only after Assembly passes and the extension is deployed + registered on Flare
mainnet: drive DEPOSIT→ORDER→WITHDRAW via the `InstructionSender` + data-provider
relay (as C8 did on Coston). **Caveat (from the 2026-07-09 bridge attempt): a
freshly-registered node may not receive relayed instructions until signing-policy /
reward-epoch propagation — register the mainnet node well ahead and confirm the
relay delivers a no-op instruction before the real run.**

### Cap ramp
Start with `HL_MAX_DEPOSIT_USD` / `HL_MAX_TOTAL_DEPOSIT_USD` at dust levels; raise
in steps (e.g. $5 → $50 → $500) only after a full deposit+withdraw cycle succeeds
and reconciliation (C6) shows the ledger matches HL within fee tolerance at each
level.

## Safety / rollback summary
- The reserve key holds custody at every intermediate hop; a failed step leaves
  funds recoverable by the reserve (swap back / bridge back / withdraw3).
- Never advance a step until the prior step's funds are confirmed recovered at
  dust scale.
- Keep `HL_RESERVE_AUTO_REFILL=false` during bring-up so every movement is explicit.
- `HL_FUNDING_MODE=bridge` for the real path; the treasury/spotSend mode is a
  testnet convenience only.

## What is NOT yet proven (open before mainnet)
- The SparkDEX WFLR/USDC.e pool depth + fee tier (confirm in C12).
- Stargate Flare→Arb One **mainnet** pool (client proven on the Arb Sepolia→Sepolia
  testnet pair; mainnet pools differ only in address, already recorded).
- Extension deployment + registration on **Flare mainnet** (Coston only so far).
- The on-chain relay-to-freshly-registered-node timing (see caveat above).
