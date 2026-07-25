# Funding Gate: Sponsored vs Self-Fund

## Problem

Test USDC supply is limited. Partners and selected testers need treasury-funded
USDC, but regular users should bring their own Hyperliquid testnet USDC.

## Design

An on-chain `sponsoredLimit` mapping in `InstructionSender.sol` gates who gets
treasury-funded test USDC and how much. The admin (contract owner) manages it
with a single transaction — no server restart, no config change.

```
InstructionSender.sol
├── mapping(address => uint256) public sponsoredLimit   ← per-user cap (6 decimals)
├── setSponsoredLimit(address user, uint256 limit)      ← owner only
├── deposit(token, amount)                              ← encodes sponsoredLimit[msg.sender]
└── confirmFunding(token, amount)                       ← self-fund users confirm HL balance
```

- `sponsoredLimit[user] = 0` → not sponsored, must self-fund
- `sponsoredLimit[user] = 5000e6` → treasury funds deposits up to $5,000 cumulative

The TEE enforces the limit: if `ledger.Balance(user) + depositUsd > limitUsd`,
the deposit falls back to the self-fund path (the user can still use the system,
they just bring their own USDC for the excess).

## Two Tiers

### Sponsored (partners / selected testers)

```
User                    InstructionSender         TEE Extension           Hyperliquid
 │                           │                        │                      │
 │── mint TestUSDC ─────────>│                        │                      │
 │── approve ───────────────>│                        │                      │
 │── deposit(token, amt) ──>│                        │                      │
 │   (escrow to Vault)      │── DEPOSIT(limit) ─────>│                      │
 │                           │                        │── check limit ───────>│
 │                           │                        │── treasury spotSend ─>│
 │                           │                        │── ledger.Credit ─────>│
 │<── ActionResult ──────────────────────────────────│                      │
 │    {hlAddress, balanceUsd}                        │                      │
```

The treasury funds the user's HL account via `spotSend`. The ledger is credited
immediately, as long as the cumulative balance stays within the sponsored limit.

### Self-fund (everyone else, or over-limit deposits)

```
User                    InstructionSender         TEE Extension           Hyperliquid
 │                           │                        │                      │
 │── mint TestUSDC ─────────>│                        │                      │
 │── approve ───────────────>│                        │                      │
 │── deposit(token, amt) ──>│                        │                      │
 │   (escrow to Vault)      │── DEPOSIT(limit=0) ───>│                      │
 │                           │                        │── derive HL address   │
 │<── ActionResult ──────────────────────────────────│                      │
 │    {selfFund: true,       │                        │                      │
 │     selfFundAddress}      │                        │                      │
 │                           │                        │                      │
 │── fund HL address via faucet / Arbitrum bridge ──────────────────────────>│
 │                           │                        │                      │
 │── confirmFunding(tok,amt)>│                        │                      │
 │                           │── CONFIRM_FUNDING ────>│                      │
 │                           │                        │── check SpotUSDC ────>│
 │                           │                        │── ledger.Credit ─────>│
 │<── ActionResult ──────────────────────────────────│                      │
 │    {hlAddress, balanceUsd}                        │                      │
```

1. User deposits TestUSDC (escrowed in the Vault as a commitment).
2. TEE returns the user's deterministically-derived HL address with `selfFund: true`.
3. User funds that HL address via the [HL testnet faucet](https://app.hyperliquid-testnet.xyz/)
   or by bridging their own Arbitrum Sepolia USDC through Bridge2.
4. User calls `confirmFunding(token, amount)` on the InstructionSender.
5. TEE verifies the HL spot balance covers the claimed amount, then credits the ledger.

Withdrawals work identically for both tiers: the TEE debits the ledger, reclaims
HL funds to the treasury, and signs a Vault slip the user submits to release
their escrowed TestUSDC.

## Admin: Managing the Allowlist

### Frontend (recommended)

The frontend has a built-in **Admin panel** (🛡 button at the bottom of the page).
It only appears for the contract owner. From there you can:

- **Set a limit**: enter a Flare address and a USD amount → "Set limit"
- **Revoke**: set the limit to 0
- **Look up**: check any user's current sponsored limit

### Contract call

```solidity
// Sponsor a partner for up to $5,000 cumulative
instructionSender.setSponsoredLimit(partnerAddress, 5000e6);

// Revoke sponsorship
instructionSender.setSponsoredLimit(partnerAddress, 0);
```

### Go tools

```go
// $5,000 limit (6 decimals)
instrutils.SetSponsoredLimit(support, instructionSenderAddr, partnerAddr, big.NewInt(5_000_000_000))

// Revoke
instrutils.SetSponsoredLimit(support, instructionSenderAddr, partnerAddr, big.NewInt(0))
```

No extension restart required. The limit is read on-chain at deposit time.

## Response Formats

### Sponsored deposit response

```json
{
  "hlAddress": "0x...",
  "balanceUsd": 100.0
}
```

### Self-fund deposit response

```json
{
  "hlAddress": "0x...",
  "selfFund": true,
  "selfFundAddress": "0x..."
}
```

`balanceUsd` is omitted (zero) until `confirmFunding` succeeds.

### confirmFunding response

```json
{
  "hlAddress": "0x...",
  "balanceUsd": 100.0
}
```

## Implementation Summary

### Contract (`InstructionSender.sol`)

- `owner` (set to deployer) + `onlyOwner` modifier
- `mapping(address => uint256) public sponsoredLimit` — per-user cumulative cap
- `setSponsoredLimit(address, uint256)` — owner-only
- `deposit()` payload: `abi.encode(token, amount, sponsoredLimit[msg.sender])`
- `confirmFunding(token, amount)` + `OP_COMMAND_CONFIRM_FUNDING`

### Extension (Go)

- `vault.go`: `processDepositVault` checks `sponsoredLimit > 0` AND
  `ledger.Balance(user) + usd <= limitUsd` to select the funding tier.
  `processConfirmFunding` verifies HL spot balance and credits the ledger.
- `config.go`: `OPCommandConfirmFunding = "CONFIRM_FUNDING"`
- `extension.go`: routes `CONFIRM_FUNDING` to `processConfirmFunding`
- `types.go`: `DepositResponse` has `SelfFund`/`SelfFundAddress` fields.
  `ConfirmFundingResponse` type.

### Frontend

- `abi.ts`: added `owner`, `sponsoredLimit`, `setSponsoredLimit` entries
- `AdminPanel.tsx`: owner-only panel to set/revoke/look up sponsored limits
- `page.tsx`: admin toggle button + AdminPanel rendering
- `SelfFundPanel.tsx`: guides non-sponsored users through self-funding
