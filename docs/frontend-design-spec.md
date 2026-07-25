# hype-accounts — Frontend Design Spec

> **Purpose.** A design handoff for the public testnet playground. It describes *what*
> to build and *how it should feel* — not the code. The plumbing already works (see
> [`frontend-testnet-plan.md`](frontend-testnet-plan.md) and the `frontend/` spike); this
> spec is about turning that spike into a product that a first-time user enjoys.
>
> **The one big idea:** trading here should feel like an **AMM swap**, not a pro perp
> terminal. We keep the two things that make it a perp — *direction* (long/short) and
> *leverage* — visible, and we hide everything else. Aim for the midpoint between a
> Uniswap swap box and a Hyperliquid trade panel, leaning toward Uniswap.

---

## 1. What this is (context for the designer)

A user connects their own Flare wallet, "deposits" test dollars, and opens leveraged
long/short positions on **real Hyperliquid testnet** markets. Under the hood every action
is a signed on-chain instruction relayed through a Trusted Execution Environment (TEE) that
trades on the user's behalf — but **the user never sees that complexity unless they want to.**

**Real vs. mocked** (only relevant to on-screen copy, not to the visuals):

| Piece | Status | What the user should understand |
|---|---|---|
| Wallet connect + signing | real | "You sign each action with your own wallet." |
| The TEE trading pipeline | real | This is the product; surface it as a friendly progress flow. |
| Hyperliquid trades / positions / PnL | real (testnet) | Live market data, real fills, real liquidations. |
| **Deposit funding (the bridge)** | **mocked** | "Test dollars, funded from a shared treasury. No real value." |
| Withdraw | mocked settlement | Funds recycle back to the treasury. |

Everything is **testnet**. No real money is ever at risk. This must be legible but not
alarming — a calm persistent banner, not a scary modal.

---

## 2. Design north star: between an AMM and a perp DEX

```
   Uniswap swap box                    ← we sit here, leaning left            Hyperliquid / GMX
   ┌───────────────┐                          ▼                          ┌────────────────────────┐
   │ You pay  100  │        ┌──────────────────────────────┐             │ orderbook │ depth │ TP │
   │   ↓           │        │  Long / Short                │             │ chart │ funding │ SL   │
   │ You get 0.05  │        │  Pay 100 USD   →  0.05 ETH    │             │ margin mode │ 50x    │
   │ [ Swap ]      │        │  Leverage ●───── 2x          │             │ isolated/cross │ ...   │
   └───────────────┘        │  [ Long ETH ]                │             └────────────────────────┘
    one action,             └──────────────────────────────┘              many controls,
    minimal params           ONE card, TWO perp levers,                   dense, expert-first
                             everything else hidden
```

**Keep front-and-center (the two perp levers):**
- **Direction** — a clean Long / Short toggle. This is the product; it stays prominent.
- **Leverage** — a lightweight slider or preset chips (1×/2×/5×/10×), default low (2×).

**Borrow the AMM's simplicity:**
- One primary **Trade card**, one primary button, one amount to think about (USD you pay).
- A "you pay → you get" mental model: *pay USD collateral → get position exposure.*
- Advanced things (limit price, exact leverage, reduce-only) live behind a collapsed
  "Advanced" affordance, exactly like AMM slippage settings hide behind a gear.

**Drop the perp-terminal furniture (out of scope for v1):**
- No order book, no depth ladder, no candlestick chart, no funding-rate tables.
- No isolated/cross margin toggle in the main flow (default cross; hide it).
- No TP/SL builder, no multi-leg orders, no margin-ratio gauges.

**The litmus test for any screen:** *Could someone who has only ever used Uniswap operate
this without reading docs?* If a control needs a tooltip to be usable, it belongs under
"Advanced" or off the screen.

---

## 3. Design principles

1. **One decision at a time.** Deposit, then trade, then manage. Never show a control the
   user can't yet act on — gate trading until the first deposit lands.
2. **Every action is async and slow (~10–20s).** This is a hard constraint, not an edge
   case. Design *around* the wait: optimistic "pending" states and a friendly progress
   flow, never a blocking full-screen spinner. See §7.
3. **Plain numbers over jargon.** "Balance $100", "You're up $4.20", "2× leverage" — not
   "account equity", "unrealized PnL", "notional 200 / margin 100".
4. **Show exposure, not just size.** When a user pays $100 at 2×, show "$200 position ·
   ≈0.05 ETH" so leverage is felt, not just typed.
5. **Failures are normal and readable.** HL rejects orders (min size, price too far,
   thin margin) routinely. Surface the reason in human language; never a silent no-op.
6. **Calm, trustworthy, a little playful.** It's testnet play money — it can be inviting
   and modern, but the TEE/self-custody story wants a credible, secure feel underneath.

---

## 4. Who uses it & the core job

- **Primary user:** a crypto-native person (has MetaMask, knows "swap") who is *new to
  perps*. They want to try a leveraged long/short without learning a pro terminal.
- **Secondary user:** an evaluator/partner watching the TEE pipeline work. For them, the
  transaction-status flow has an expandable "under the hood" view (§7.4).

**The core job, in one sentence:** *"Let me put $50 into a 3× long on ETH, watch it move,
and close it — in under a minute, without reading anything."*

---

## 5. Information architecture & layout

Single-page app. No routing between pages; everything is one scroll (or a two-column
desktop layout). Think Uniswap, not a dashboard with a nav rail.

**Desktop (≥ 900px)** — two columns, trade on the left (the hero), account on the right:

```
┌───────────────────────────────────────────────────────────────────────────┐
│  hype-accounts        [ Coston2 ▾ ]   Balance $100.00   [ 0x1a2…9f ▾ ]      │  ← header
├───────────────────────────────────────────────────────────────────────────┤
│  ⚠ Testnet · deposits are mock-funded from a treasury. No real value.       │  ← banner
├──────────────────────────────────┬────────────────────────────────────────┤
│                                  │  YOUR MONEY                             │
│   ┌── TRADE ──────────────────┐  │  Balance      $100.00   [Deposit][…]    │
│   │  Market   ETH ▾   $3,412  │  │  ─────────────────────────────────────  │
│   │  [ Long ] [ Short ]       │  │  POSITIONS                              │
│   │  You pay   [  50  ] USD   │  │  ┌───────────────────────────────────┐  │
│   │  Leverage  ●────── 3×     │  │  │ ETH  Long 3×      +$4.20  ▲        │  │
│   │  ≈ 0.044 ETH · $150 pos   │  │  │ 0.044 · entry 3,400  [ Close ]    │  │
│   │  Advanced ▾               │  │  └───────────────────────────────────┘  │
│   │  [   Long ETH   ]         │  │  OPEN ORDERS                            │
│   └───────────────────────────┘  │  (none)                                 │
│                                  │                                         │
└──────────────────────────────────┴────────────────────────────────────────┘
        (transaction status appears as a toast / drawer, §7)
```

**Mobile (< 900px)** — single column, stacked in job order:
`Header → Banner → Balance strip → Trade card → Positions → Open orders`.
The Trade card is the top of the fold. Deposit/Withdraw open as bottom sheets.

---

## 6. Component-by-component spec

### 6.1 Header

- **Logo/wordmark** (left).
- **Network selector / badge** — shows the active testnet (Coston2). If the wallet is on
  the wrong chain, this becomes an amber **"Switch to Coston2"** action.
- **Balance** — the deposited USD balance, shown as a first-class number (this is the
  number the user cares about most). Clicking it can open Deposit.
- **Wallet control** — connect button when disconnected; truncated address + menu
  (copy, view on explorer, disconnect) when connected.

### 6.2 Onboarding & connect (first run)

- **Disconnected state:** a friendly hero — one line of what this is ("Try leveraged
  trading on Hyperliquid testnet with play money") and a single **Connect wallet** button.
- **First-run explainer:** a lightweight, dismissible card or 2–3 step intro covering:
  (1) it's testnet play money, (2) you sign each action yourself, (3) actions take ~10–20s
  because they route through a secure enclave. Keep it skippable and short.
- **Gas help:** users pay a tiny network fee in test gas (C2FLR). Provide a **"Get test
  gas"** faucet link and a clear "not enough gas" state when their wallet is empty.

### 6.3 Deposit (mock funding)

The AMM-simple "add money" flow. Inline card or bottom sheet.

- Amount input + **quick chips** ($25 / $50 / $100 / Max-to-cap).
- **Per-address cap** enforced and shown ("Up to $100 per wallet on testnet").
- Copy makes the mock explicit but friendly: *"Test dollars, funded from a shared
  treasury. Instant-ish (~15s)."*
- Single **Deposit** button → triggers the async status flow (§7).
- On success: balance animates up; if it's the **first** deposit, reveal the trade UI and
  (optionally) show the derived Hyperliquid address once, with an explorer link, framed as
  "Your testnet trading account."

### 6.4 The Trade card — the centerpiece

This is where the "between AMM and perp" idea lives. Detailed anatomy, top to bottom:

1. **Market selector.** A compact dropdown/pill (ETH, BTC, SOL, HYPE, …) with the **live
   mid price** shown inline and gently updating. No chart. Searchable if the list grows.
2. **Direction toggle.** A prominent segmented **Long / Short** control. Green long / red
   short. This is the emotional center of the card — make it satisfying to flip.
3. **"You pay" amount.** USD collateral from the user's balance. Big, focused input with a
   **Max** button. This is the AMM "You pay" field.
4. **Leverage.** A lightweight slider *or* preset chips **1× / 2× / 5× / 10×** with a
   default of **2×**. Keep the range friendly (cap the default UI at ~10×; exact/higher
   values go under Advanced). Label it "Leverage", not "margin".
5. **Derived exposure line** (the AMM "You get").** A quiet, auto-updating readout:
   `≈ 0.044 ETH · $150 position`. Shows what the pay+leverage actually buys. Optionally a
   second faint line: `est. liquidation ~$2,900` and `est. fee`.
6. **Advanced (collapsed by default).** A disclosure that reveals:
   - **Limit price** (turns the order from Market → Limit). Prefill with the live mid.
   - **Exact leverage** entry.
   - **Reduce-only** toggle (for closing/trimming — mostly used from the position card
     instead, so this is secondary here).
   - Margin mode stays hidden/default (cross) in v1.
7. **Primary CTA.** One full-width button, colored by direction and stating intent:
   **"Long ETH"** / **"Short ETH"**. Disabled with a reason when invalid (see below).
8. **Helper microcopy.** One line: fills are at market; HL needs ≈$10+ position size and
   enough balance.

**Validation the card must handle (inline, before submit):**
- Amount > available balance → "Not enough balance — deposit more."
- Position size below HL minimum (~$10 notional) → "Increase size or leverage."
- **Size-decimals rule:** some coins require whole-number sizes (e.g. PURR); round/enforce
  to the coin's allowed precision so orders aren't rejected downstream.
- Limit price empty/whitespace in limit mode → block with a hint.

**Spot vs perp:** v1 is **perp-first** (the spike only does perp). If spot is included,
express it as a tiny mode switch at the top of the card ("Perp / Spot"), where Spot hides
leverage entirely and reads *exactly* like a swap. Treat spot as a nice-to-have; don't let
it complicate the perp default. (Flag — see Open questions §12.)

### 6.5 Positions

Approachable **position cards**, not a dense pro table. One card per open position:

- **Header:** coin + a **Long/Short** badge + leverage (`ETH · Long · 3×`).
- **Hero number:** unrealized PnL, large and color-coded (`+$4.20 ▲` green / `−$2.10 ▼`
  red). This is what users stare at.
- **Sub-line:** size and entry (`0.044 ETH · entry $3,400 · mark $3,412`).
- **Actions:** a primary **Close** button (market reduce-only, one tap). Optionally a
  **Close 50%** quick action. Closing runs the same async status flow.
- **Empty state:** a warm nudge — "No open positions yet. Make your first trade above."

On desktop a compact table is acceptable *if* it stays light (coin, direction, PnL, close),
but cards are preferred for the friendly tone and for mobile.

### 6.6 Open orders (limit orders resting)

- Only appears when the user has resting limit orders. Simple list rows: coin, side, size,
  price, and a **Cancel** button (async flow). Empty = hidden or a single muted "none".

### 6.7 Withdraw

- Mirror Deposit: amount input, **Max** (= withdrawable), single button.
- Note the small HL fee (~1 USDC) and that it's a mock settlement back to the treasury.
- Same async status flow; balance animates down on success.

---

## 7. The async pipeline — the most important interaction

Every write action (deposit, trade, close, cancel, withdraw) is a multi-step async job that
takes **~10–20 seconds**: sign in wallet → mine on Flare → relayed through data providers to
the TEE → executed on Hyperliquid → result. **The whole UX must be built around this wait.**
It's also the product's signature "wow" — the pipeline is real — so present it as reassuring
progress, not dead time.

### 7.1 Pattern: a progress toast/drawer, not a blocking spinner

When an action starts, the user is **not** blocked — the app stays usable, and a compact
**status card** appears (bottom-right toast on desktop, bottom sheet on mobile) tracking the
one in-flight action. It reads like a friendly swap confirmation:

```
┌── Long 0.044 ETH · 3× ─────────────┐
│ ● Signed & submitted        ✓      │
│ ● Confirming on Flare       ✓      │
│ ◐ Executing on Hyperliquid  …      │   ← animated
│ ○ Done                             │
│                     Details ▾      │
└────────────────────────────────────┘
```

### 7.2 The four user-facing steps (rename the plumbing)

| Internal phase | User-facing label | Notes |
|---|---|---|
| signing | "Sign in your wallet" | waiting on the wallet popup |
| mining | "Confirming on Flare" | tx receipt; show explorer link |
| relaying (poll) | "Executing on Hyperliquid" | the ~12s relay + fill; longest step |
| done / error | "Done" / a plain-English error | success animates balance/positions |

### 7.3 States & feel

- **Optimistic hints:** e.g. after a Close is submitted, dim the position card and label it
  "Closing…" rather than leaving it looking open.
- **Never a full-screen block.** The user can start reading positions, queue nothing else
  dangerous, and watch progress in the corner.
- **Success:** a light celebratory beat (checkmark, subtle balance count-up). Auto-dismiss
  after a few seconds, or let the user dismiss.
- **Failure:** the card turns to an error state with a **human-readable reason** parsed from
  the result log (e.g. "Order too small — Hyperliquid needs ~$10+", "Price too far from
  market", "Not enough margin"). Offer a "Try again" that reopens the trade card prefilled.
- **Timeout:** if the relay never resolves, say so calmly and link to the explorer /
  details rather than spinning forever.

### 7.4 "Details" — the under-the-hood view (for evaluators)

Behind a **Details ▾** disclosure on the status card: the literal pipeline timeline
(submitted → mined → relayed → executed), the `instructionId`, the Flare tx hash with
explorer link, and the decoded raw result JSON. This is off by default but is the demo's
credibility payload for technical viewers. Keep it visually distinct (mono, muted) from the
friendly layer.

---

## 8. Data reference (what to show, where it comes from, how to format)

All read data comes live from Hyperliquid's public API keyed on the user's derived address,
polled every ~5s. Writes go through the on-chain instruction + result poll.

| Surface | Field | Source | Format |
|---|---|---|---|
| Header balance | spot USDC balance | HL `spotClearinghouseState` | `$100.00` |
| Trade "you get" | mid price → size | HL `allMids` | `≈0.044 ETH · $150 position` |
| Position PnL | unrealized PnL | HL `clearinghouseState` | `+$4.20` / `−$2.10`, colored |
| Position | size / entry / mark | HL positions + mids | `0.044 ETH · entry $3,400` |
| Withdraw max | withdrawable | HL `clearinghouseState` | `$96.30` |
| Open orders | side / size / price / id | HL `openOrders` | list rows |
| Status details | instructionId, result JSON | TEE proxy result poll | mono/raw |

**Formatting rules:** USD to 2 decimals; coin sizes to the coin's own precision; prices with
sensible significant figures; always sign PnL and color it; show a subtle "updated Xs ago" or
a gentle pulse so users trust the numbers are live; skeleton-load numbers, never show `$0.00`
while actually loading.

---

## 9. State matrix (design every one of these)

| Surface | Empty | Loading | Error | Disabled/gated |
|---|---|---|---|---|
| Whole app | disconnected hero | — | — | wrong network → switch prompt |
| Balance | `$0.00` + "Deposit to start" | skeleton | "couldn't load balance, retry" | — |
| Trade card | — | mid price shimmer | — | gated until first deposit; per-field validation |
| Positions | "No positions yet" nudge | skeleton rows | read-error inline | actions disabled while an action is in flight |
| Orders | hidden / "none" | skeleton | inline | — |
| Status card | not shown | animated | human-readable failure + retry | — |
| Deposit/Withdraw | — | button → pending | cap exceeded / insufficient | disabled when over cap or 0 |

Also design: **insufficient gas** (needs faucet), **wallet on wrong network**, **action
already in flight** (one at a time), and **treasury-empty** (deposits temporarily paused —
show a graceful "testnet is busy, try later").

---

## 10. Guardrails & testnet framing

- **Persistent testnet banner** (calm amber strip): testnet + mock-funded deposits. Always
  visible, never a modal after first run.
- **Per-address deposit cap** (e.g. $100) surfaced in the deposit UI, not just enforced.
- **One in-flight action at a time** — a gentle constraint that keeps the async story
  legible and the UI honest.
- Optional **access gate** (invite code) for a first cohort — if used, a simple gate screen
  before connect. (Flag — Open questions §12.)

---

## 11. Visual direction

The `frontend/` spike ships a **dark, calm, fintech palette** — treat it as a *starting
point*, not a constraint. You're encouraged to elevate it into something distinctive.

- **Current palette (reference):** near-black background `#0b0e14`, panel `#141922`, text
  `#e6e9ef`, muted `#8b94a7`, accent blue `#5b8cff`, green `#3fb950`, red `#f85149`, amber
  `#d29922`. Rounded 8–12px cards, subtle borders, generous spacing.
- **Tone:** modern, trustworthy, a touch playful (it's play money). Think a friendly
  neobank meets a clean DEX — not a Bloomberg terminal, not a memecoin casino.
- **Direction color language:** green = long/up, red = short/down, used consistently across
  the toggle, PnL, and position badges.
- **Motion:** purposeful and tied to real events — the balance count-up on deposit, the
  status-step progression, a satisfying long/short toggle. Motion should explain the async
  latency, never decorate emptiness. Respect `prefers-reduced-motion`.
- **Density:** low. Lots of breathing room. The Trade card and PnL numbers are the heroes;
  everything else recedes.
- **Light mode:** provide one; testnet users span both. Theme-aware, not dark-only.

---

## 12. Responsive & accessibility

- **Mobile-first single column**; the Trade card and primary CTA must be reachable and
  tappable one-handed. Deposit/Withdraw/Status as bottom sheets.
- Full **keyboard operability**; visible focus rings; the long/short toggle and leverage
  control must be keyboard- and screen-reader-usable (proper roles/labels).
- **Color is never the only signal** — pair green/red with +/− and ▲/▼ so PnL and direction
  read without color vision.
- Numbers use tabular figures so they don't jitter as they update live.
- Any horizontally wide content (tables, raw result JSON) scrolls inside its own container;
  the page body never scrolls sideways.

---

## 13. Out of scope for v1 (say no on purpose)

Order book / depth · price charts · funding-rate displays · isolated/cross margin editing ·
take-profit / stop-loss builder · advanced order types (stop, scaled, TWAP) · multi-network
switching · trade history/analytics · social/leaderboard. These are what make it a *perp
terminal*; leaving them out is the point. Revisit post-v1.

---

## 14. Open questions for product (please confirm before final design)

1. **Spot trading in v1?** The spike is perp-only. Include a Spot mode (pure swap, no
   leverage), or ship perp-only and add spot later? *(Recommend: perp-only v1.)*
2. **Single page vs. tabs?** This spec assumes one page (trade + account together). OK, or
   do you want a separate Portfolio view?
3. **Position display:** cards (recommended, friendlier) or a compact table on desktop?
4. **Access gate:** open testnet, or invite-code gated for a first cohort?
5. **Brand:** is "hype-accounts" the shipped name, or does this need a product name/logo?
6. **Leverage ceiling** shown in the default UI (10×? 20×?) before "Advanced" is needed.
7. **How loud is the TEE story?** Keep the pipeline as a quiet "Details" view, or make the
   secure-enclave narrative a visible selling point on the page?
