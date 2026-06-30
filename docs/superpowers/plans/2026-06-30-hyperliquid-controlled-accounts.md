# Hyperliquid Controlled Accounts Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a Flare Compute Extension where users trade spot + perps on Hyperliquid testnet via plain on-chain instructions; the TEE holds a dedicated per-user HL account, funded from a treasury, and executes deposits/orders/cancels/withdrawals on their behalf.

**Architecture:** On-chain `HyperliquidInstructionSender.sol` emits one OPType `HL` with four OPCommands (`DEPOSIT`, `PLACE_ORDER`, `CANCEL`, `WITHDRAW`), embedding `msg.sender` into the instruction message via `abi.encode(msg.sender, payload)`. The TEE extension recovers the user, deterministically derives that user's HL key from a master seed (HKDF), wraps it in a `go-hyperliquid` Exchange, and performs the action on HL testnet. An in-memory ledger tracks per-user balances and recent orders. HL network calls run via the existing async pattern (return `pending`, post the real result to the tee-node `/result` port from a goroutine).

**Tech Stack:** Go 1.25.1, `github.com/sonirico/go-hyperliquid` v0.33.1, `github.com/ethereum/go-ethereum` (crypto, abi), `golang.org/x/crypto/hkdf`, Solidity ^0.8.27 (Foundry), `github.com/flare-foundation/{go-flare-common,tee-node}`.

## Global Constraints

- Go module name stays `extension-scaffold` (imports use `extension-scaffold/...`). Only the **contract** and its **bindings package** are renamed (HelloWorld → Hyperliquid), per the rename-scaffold workflow.
- Go version floor: **1.25.1** (from `go.mod`).
- `go-hyperliquid` version: **v0.33.1** (already in the prior WIP; pin this exact version).
- HL endpoint for all live calls: **`https://api.hyperliquid-testnet.xyz`** (testnet). The library signs for testnet only when `baseURL != MainnetAPIURL`, so the testnet URL is mandatory.
- OPType/OPCommand strings MUST match byte-for-byte between Go (`internal/config/config.go`) and Solidity (`contracts/InstructionSender.sol`).
- Instruction message envelope is ALWAYS `abi.encode(address user, bytes payload)`; `payload` is JSON.
- Secrets (`HL_MASTER_SEED`, `HL_TREASURY_KEY`) come from env only; never commit real values. Update `.env.example` only — do NOT create or edit `.env` without explicit user approval.
- `buildResult` status codes: `0` = error (message → `Log`), `1` = success (`data` → `Data`), `2` = pending (async).
- Do NOT modify infrastructure marked "DO NOT MODIFY" (`New()`, `actionHandler()`, `cmd/main.go`, `pkg/server/`, the `setExtensionId`/constructor in the contract).
- Minimum HL order ≈ $10 notional; design test orders above this.
- TDD throughout: write the failing test, see it fail, implement minimally, see it pass, commit. Commits are local only — do NOT push.

## File Structure

**New files**
- `internal/keys/derive.go` — HKDF derivation of a per-user secp256k1 key from master seed + Flare address. Pure, no network.
- `internal/keys/derive_test.go`
- `internal/ledger/ledger.go` — in-memory per-user balances + recent orders, mutex-guarded.
- `internal/ledger/ledger_test.go`
- `internal/hl/account.go` — `Account` interface, `OrderReq`/`OrderResult`/`PositionsSnapshot` types, `Exchange` factory interface, real impl wrapping `go-hyperliquid`.
- `internal/hl/fake.go` — `FakeAccount`/`FakeExchange` test doubles (build tag-free; used by extension tests).
- `internal/extension/processors.go` — the four handlers + async helper + result poster (harvested/adapted from the prior WIP).
- `internal/extension/processors_test.go`
- `testing/hl_testnet_test.go` — build-tagged integration test against real HL testnet.

**Modified files**
- `internal/config/config.go` — OPType/OPCommands + HL config (URL, seed, treasury, caps, slippage).
- `pkg/types/types.go` — request/response/state structs + the `(address,bytes)` envelope ABI argument.
- `pkg/types/register.go` — decoder registrations for the four commands.
- `internal/extension/extension.go` — `Extension` struct (seed, treasury, ledger, factory, account cache), router, `stateHandler`.
- `internal/extension/utils.go` — add `sendResult`/retrying client (harvested from prior WIP `internal/utils`).
- `contracts/InstructionSender.sol` — rename + OP constants + four send functions embedding `msg.sender`.
- `scripts/generate-bindings.sh`, `tools/pkg/contracts/hyperliquid/*` — renamed bindings.
- `tools/pkg/utils/instructions.go` — send helpers for the four commands.
- `tools/cmd/run-test/main.go` — end-to-end send + poll harness.
- `.env.example`, `README.md` — HL config + run instructions.
- `go.mod` — add `go-hyperliquid` and `golang.org/x/crypto`.

---

### Task 1: Rename scaffold HelloWorld → Hyperliquid

**Files:**
- Modify: `contracts/InstructionSender.sol`
- Modify: `scripts/generate-bindings.sh`
- Rename: `tools/pkg/contracts/helloworld/` → `tools/pkg/contracts/hyperliquid/`
- Rename: `tools/pkg/contracts/hyperliquid/helloworld.go` → `hyperliquid.go`
- Modify: `tools/pkg/utils/instructions.go`

**Interfaces:**
- Produces: contract `HyperliquidInstructionSender`; Go bindings package `hyperliquid` (type `HyperliquidInstructionSender`, `DeployHyperliquidInstructionSender`, `NewHyperliquidInstructionSender`, `HyperliquidInstructionSenderMetaData`).

- [ ] **Step 1: Rename the Solidity contract identifier**

In `contracts/InstructionSender.sol`, replace all `HelloWorldInstructionSender` with `HyperliquidInstructionSender` (`replace_all: true`). Do not touch OP constants or logic yet.

- [ ] **Step 2: Update generate-bindings.sh**

In `scripts/generate-bindings.sh` set `CONTRACT_NAME="HyperliquidInstructionSender"` and `GO_PKG="hyperliquid"`.

- [ ] **Step 3: Move bindings dir and go:generate file**

Run:
```bash
cd /Users/snojj25/Desktop/505/Flare/tee/extension-examples/hype-accounts && git mv tools/pkg/contracts/helloworld tools/pkg/contracts/hyperliquid && git mv tools/pkg/contracts/hyperliquid/helloworld.go tools/pkg/contracts/hyperliquid/hyperliquid.go
```

- [ ] **Step 4: Update the go:generate directive**

Set `tools/pkg/contracts/hyperliquid/hyperliquid.go` to exactly:
```go
//go:generate go run github.com/ethereum/go-ethereum/cmd/abigen --abi=HyperliquidInstructionSender.abi --bin=HyperliquidInstructionSender.bin --pkg=hyperliquid --type=HyperliquidInstructionSender --out=autogen.go

package hyperliquid
```

- [ ] **Step 5: Update tools imports/types**

In `tools/pkg/utils/instructions.go`: change import `extension-scaffold/tools/pkg/contracts/helloworld` → `.../hyperliquid`; replace `helloworld.` → `hyperliquid.` (`replace_all: true`); replace `HelloWorldInstructionSender` → `HyperliquidInstructionSender` (`replace_all: true`). (The `SendSayHello`/`SendSayGoodbye` helpers are rewritten in Task 14; leave them compiling for now.)

- [ ] **Step 6: Generate bindings and verify build**

Run:
```bash
cd /Users/snojj25/Desktop/505/Flare/tee/extension-examples/hype-accounts && ./scripts/generate-bindings.sh && cd tools && go build ./...
```
Expected: bindings regenerate; `go build ./...` succeeds (helloworld send functions still exist on the contract at this point, so bindings match).

- [ ] **Step 7: Commit**

```bash
cd /Users/snojj25/Desktop/505/Flare/tee/extension-examples/hype-accounts && git add -A && git commit -m "refactor: rename scaffold contract and bindings to Hyperliquid"
```

---

### Task 2: Config — OPType, OPCommands, and HL settings

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go` (create)

**Interfaces:**
- Produces: constants `OPTypeHL = "HL"`, `OPCommandDeposit = "DEPOSIT"`, `OPCommandPlaceOrder = "PLACE_ORDER"`, `OPCommandCancel = "CANCEL"`, `OPCommandWithdraw = "WITHDRAW"`; vars `HyperliquidURL`, `MasterSeedHex`, `TreasuryKeyHex`, `MaxDepositUSD`, `MaxTotalDepositUSD`, `MarketSlippage`; func `LoadHLConfig() error`.

- [ ] **Step 1: Write the failing test**

Create `internal/config/config_test.go`:
```go
package config

import (
	"os"
	"testing"
)

func TestLoadHLConfigReadsEnv(t *testing.T) {
	os.Setenv("HYPERLIQUID_URL", "https://api.hyperliquid-testnet.xyz")
	os.Setenv("HL_MASTER_SEED", "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff")
	os.Setenv("HL_TREASURY_KEY", "1111111111111111111111111111111111111111111111111111111111111111")
	os.Setenv("HL_MAX_DEPOSIT_USD", "250")
	defer func() {
		os.Unsetenv("HYPERLIQUID_URL")
		os.Unsetenv("HL_MASTER_SEED")
		os.Unsetenv("HL_TREASURY_KEY")
		os.Unsetenv("HL_MAX_DEPOSIT_USD")
	}()

	if err := LoadHLConfig(); err != nil {
		t.Fatalf("LoadHLConfig: %v", err)
	}
	if HyperliquidURL != "https://api.hyperliquid-testnet.xyz" {
		t.Errorf("url = %q", HyperliquidURL)
	}
	if MasterSeedHex == "" || TreasuryKeyHex == "" {
		t.Errorf("seed/treasury not loaded")
	}
	if MaxDepositUSD != 250 {
		t.Errorf("MaxDepositUSD = %v", MaxDepositUSD)
	}
}

func TestLoadHLConfigRequiresSecrets(t *testing.T) {
	os.Unsetenv("HL_MASTER_SEED")
	os.Unsetenv("HL_TREASURY_KEY")
	if err := LoadHLConfig(); err == nil {
		t.Fatal("expected error when secrets missing")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/snojj25/Desktop/505/Flare/tee/extension-examples/hype-accounts && go test ./internal/config/ -run TestLoadHLConfig -v`
Expected: FAIL — `LoadHLConfig`, constants, and vars undefined.

- [ ] **Step 3: Implement config additions**

In `internal/config/config.go`, replace the greeting constants block and add HL settings:
```go
const (
	Version = "0.1.0"

	OPTypeHL = "HL"

	OPCommandDeposit    = "DEPOSIT"
	OPCommandPlaceOrder = "PLACE_ORDER"
	OPCommandCancel     = "CANCEL"
	OPCommandWithdraw   = "WITHDRAW"

	TimeoutShutdown = 5 * time.Second
)

// HL settings (populated by LoadHLConfig).
var (
	HyperliquidURL     = "https://api.hyperliquid-testnet.xyz"
	MasterSeedHex      string
	TreasuryKeyHex     string
	MaxDepositUSD      = 1000.0
	MaxTotalDepositUSD = 5000.0
	MarketSlippage     = 0.05
)

// LoadHLConfig reads HL-specific settings from the environment.
// Returns an error if required secrets are missing.
func LoadHLConfig() error {
	if v := os.Getenv("HYPERLIQUID_URL"); v != "" {
		HyperliquidURL = v
	}
	MasterSeedHex = os.Getenv("HL_MASTER_SEED")
	TreasuryKeyHex = os.Getenv("HL_TREASURY_KEY")
	if MasterSeedHex == "" {
		return fmt.Errorf("HL_MASTER_SEED is required")
	}
	if TreasuryKeyHex == "" {
		return fmt.Errorf("HL_TREASURY_KEY is required")
	}
	if v := os.Getenv("HL_MAX_DEPOSIT_USD"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			MaxDepositUSD = f
		}
	}
	if v := os.Getenv("HL_MAX_TOTAL_DEPOSIT_USD"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			MaxTotalDepositUSD = f
		}
	}
	return nil
}
```
Add `"fmt"` to imports (keep `os`, `strconv`, `time`). Leave the existing port `init()` untouched.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/snojj25/Desktop/505/Flare/tee/extension-examples/hype-accounts && go test ./internal/config/ -v`
Expected: PASS. (Note: `extension.go` still references removed greeting constants and will not build yet — that is fixed in Task 7. `go test ./internal/config/` compiles only the config package and passes.)

- [ ] **Step 5: Commit**

```bash
cd /Users/snojj25/Desktop/505/Flare/tee/extension-examples/hype-accounts && git add internal/config/ && git commit -m "feat: add HL OPType/OPCommands and config loader"
```

---

### Task 2.5: Add dependencies

**Files:**
- Modify: `go.mod`, `go.sum`

- [ ] **Step 1: Add modules**

Run:
```bash
cd /Users/snojj25/Desktop/505/Flare/tee/extension-examples/hype-accounts && go get github.com/sonirico/go-hyperliquid@v0.33.1 && go get golang.org/x/crypto/hkdf && go mod tidy
```
Expected: `go.mod` now requires `github.com/sonirico/go-hyperliquid v0.33.1` and `golang.org/x/crypto`.

- [ ] **Step 2: Commit**

```bash
cd /Users/snojj25/Desktop/505/Flare/tee/extension-examples/hype-accounts && git add go.mod go.sum && git commit -m "build: add go-hyperliquid and x/crypto deps"
```

---

### Task 3: Per-user key derivation (`internal/keys`)

**Files:**
- Create: `internal/keys/derive.go`
- Test: `internal/keys/derive_test.go`

**Interfaces:**
- Produces: `func DeriveUserKey(masterSeed []byte, user common.Address) (*ecdsa.PrivateKey, error)`; `func MustHexSeed(hexSeed string) ([]byte, error)`.

- [ ] **Step 1: Write the failing test**

Create `internal/keys/derive_test.go`:
```go
package keys

import (
	"bytes"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

var seed = bytes.Repeat([]byte{0xAB}, 32)

func TestDeriveDeterministic(t *testing.T) {
	u := common.HexToAddress("0x1111111111111111111111111111111111111111")
	k1, err := DeriveUserKey(seed, u)
	if err != nil {
		t.Fatal(err)
	}
	k2, err := DeriveUserKey(seed, u)
	if err != nil {
		t.Fatal(err)
	}
	if crypto.PubkeyToAddress(k1.PublicKey) != crypto.PubkeyToAddress(k2.PublicKey) {
		t.Fatal("derivation not deterministic")
	}
}

func TestDeriveDistinctUsers(t *testing.T) {
	a := common.HexToAddress("0x1111111111111111111111111111111111111111")
	b := common.HexToAddress("0x2222222222222222222222222222222222222222")
	ka, _ := DeriveUserKey(seed, a)
	kb, _ := DeriveUserKey(seed, b)
	if crypto.PubkeyToAddress(ka.PublicKey) == crypto.PubkeyToAddress(kb.PublicKey) {
		t.Fatal("distinct users produced same key")
	}
}

func TestDeriveValidKey(t *testing.T) {
	u := common.HexToAddress("0x3333333333333333333333333333333333333333")
	k, err := DeriveUserKey(seed, u)
	if err != nil {
		t.Fatal(err)
	}
	if k.D.Sign() == 0 {
		t.Fatal("zero scalar")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/snojj25/Desktop/505/Flare/tee/extension-examples/hype-accounts && go test ./internal/keys/ -v`
Expected: FAIL — `DeriveUserKey` undefined.

- [ ] **Step 3: Implement derivation**

Create `internal/keys/derive.go`:
```go
// Package keys derives deterministic per-user Hyperliquid signing keys
// from a TEE-held master seed. Same (seed, user) always yields the same key.
package keys

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"golang.org/x/crypto/hkdf"
)

// MustHexSeed decodes a hex master seed and requires at least 32 bytes.
func MustHexSeed(hexSeed string) ([]byte, error) {
	b, err := hex.DecodeString(strings.TrimPrefix(hexSeed, "0x"))
	if err != nil {
		return nil, fmt.Errorf("seed not hex: %w", err)
	}
	if len(b) < 32 {
		return nil, fmt.Errorf("seed must be >= 32 bytes, got %d", len(b))
	}
	return b, nil
}

// DeriveUserKey derives the secp256k1 private key for a user via HKDF-SHA256.
// A counter byte appended to the info string handles the rare case where the
// derived 32 bytes are not a valid secp256k1 scalar.
func DeriveUserKey(masterSeed []byte, user common.Address) (*ecdsa.PrivateKey, error) {
	base := "hl-account:" + strings.ToLower(user.Hex())
	for counter := byte(0); counter < 255; counter++ {
		info := append([]byte(base), counter)
		r := hkdf.New(sha256.New, masterSeed, nil, info)
		out := make([]byte, 32)
		if _, err := io.ReadFull(r, out); err != nil {
			return nil, err
		}
		key, err := crypto.ToECDSA(out) // validates range [1, n-1]
		if err == nil {
			return key, nil
		}
	}
	return nil, fmt.Errorf("failed to derive valid key for %s", user.Hex())
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/snojj25/Desktop/505/Flare/tee/extension-examples/hype-accounts && go test ./internal/keys/ -v`
Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
cd /Users/snojj25/Desktop/505/Flare/tee/extension-examples/hype-accounts && git add internal/keys/ && git commit -m "feat: deterministic per-user HL key derivation"
```

---

### Task 4: Per-user ledger (`internal/ledger`)

**Files:**
- Create: `internal/ledger/ledger.go`
- Test: `internal/ledger/ledger_test.go`

**Interfaces:**
- Produces: `type OrderRecord struct{ InstructionID, Coin, Market string; IsBuy bool; Size float64; Status string }`; `type Snapshot struct{ BalanceUSD float64; RecentOrders []OrderRecord }`; `type Ledger` with `New() *Ledger`, `Credit(addr, amt)`, `Debit(addr, amt) error`, `Balance(addr) float64`, `Total() float64`, `AddOrder(addr, OrderRecord)`, `SnapshotFor(addr) Snapshot`.

- [ ] **Step 1: Write the failing test**

Create `internal/ledger/ledger_test.go`:
```go
package ledger

import (
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

var u = common.HexToAddress("0xabc0000000000000000000000000000000000001")

func TestCreditDebit(t *testing.T) {
	l := New()
	l.Credit(u, 100)
	if l.Balance(u) != 100 {
		t.Fatalf("balance = %v", l.Balance(u))
	}
	if err := l.Debit(u, 40); err != nil {
		t.Fatal(err)
	}
	if l.Balance(u) != 60 {
		t.Fatalf("balance = %v", l.Balance(u))
	}
}

func TestDebitInsufficient(t *testing.T) {
	l := New()
	l.Credit(u, 10)
	if err := l.Debit(u, 25); err == nil {
		t.Fatal("expected insufficient-funds error")
	}
}

func TestTotalAndOrders(t *testing.T) {
	l := New()
	l.Credit(u, 50)
	l.Credit(common.HexToAddress("0xdef"), 30)
	if l.Total() != 80 {
		t.Fatalf("total = %v", l.Total())
	}
	l.AddOrder(u, OrderRecord{Coin: "BTC", IsBuy: true, Size: 0.001, Status: "submitted"})
	s := l.SnapshotFor(u)
	if s.BalanceUSD != 50 || len(s.RecentOrders) != 1 {
		t.Fatalf("snapshot = %+v", s)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/snojj25/Desktop/505/Flare/tee/extension-examples/hype-accounts && go test ./internal/ledger/ -v`
Expected: FAIL — undefined symbols.

- [ ] **Step 3: Implement ledger**

Create `internal/ledger/ledger.go`:
```go
// Package ledger tracks per-user USD balances and recent orders in memory.
package ledger

import (
	"fmt"
	"sync"

	"github.com/ethereum/go-ethereum/common"
)

type OrderRecord struct {
	InstructionID string  `json:"instructionId"`
	Coin          string  `json:"coin"`
	Market        string  `json:"market"`
	IsBuy         bool    `json:"isBuy"`
	Size          float64 `json:"size"`
	Status        string  `json:"status"`
}

type Snapshot struct {
	BalanceUSD   float64       `json:"balanceUsd"`
	RecentOrders []OrderRecord `json:"recentOrders"`
}

type Ledger struct {
	mu       sync.RWMutex
	balances map[common.Address]float64
	orders   map[common.Address][]OrderRecord
}

func New() *Ledger {
	return &Ledger{
		balances: map[common.Address]float64{},
		orders:   map[common.Address][]OrderRecord{},
	}
}

func (l *Ledger) Credit(addr common.Address, amt float64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.balances[addr] += amt
}

func (l *Ledger) Debit(addr common.Address, amt float64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.balances[addr] < amt {
		return fmt.Errorf("insufficient balance: have %.2f, need %.2f", l.balances[addr], amt)
	}
	l.balances[addr] -= amt
	return nil
}

func (l *Ledger) Balance(addr common.Address) float64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.balances[addr]
}

func (l *Ledger) Total() float64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	var t float64
	for _, v := range l.balances {
		t += v
	}
	return t
}

func (l *Ledger) AddOrder(addr common.Address, rec OrderRecord) {
	l.mu.Lock()
	defer l.mu.Unlock()
	o := append(l.orders[addr], rec)
	if len(o) > 20 {
		o = o[len(o)-20:]
	}
	l.orders[addr] = o
}

func (l *Ledger) SnapshotFor(addr common.Address) Snapshot {
	l.mu.RLock()
	defer l.mu.RUnlock()
	recent := make([]OrderRecord, len(l.orders[addr]))
	copy(recent, l.orders[addr])
	return Snapshot{BalanceUSD: l.balances[addr], RecentOrders: recent}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/snojj25/Desktop/505/Flare/tee/extension-examples/hype-accounts && go test ./internal/ledger/ -v`
Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
cd /Users/snojj25/Desktop/505/Flare/tee/extension-examples/hype-accounts && git add internal/ledger/ && git commit -m "feat: in-memory per-user ledger"
```

---

### Task 5: HL account adapter + factory + fakes (`internal/hl`)

**Files:**
- Create: `internal/hl/account.go`
- Create: `internal/hl/fake.go`
- Test: `internal/hl/account_test.go`

**Interfaces:**
- Produces:
  - `type OrderReq struct{ Coin string; Market string; IsBuy bool; OrderType string; Size float64; LimitPx float64; ReduceOnly bool; Leverage int; IsCross bool }`
  - `type OrderResult struct{ Raw json.RawMessage; OrderID int64 }`
  - `type Position struct{ Coin string; Szi float64; EntryPx float64; UnrealizedPnl float64 }`
  - `type PositionsSnapshot struct{ Perp []Position; PerpAccountValueUSD float64; SpotUSDC float64 }`
  - `type Account interface { Address() string; PlaceOrder(ctx, OrderReq) (OrderResult, error); Cancel(ctx, coin string, oid int64) error; SendUSD(ctx, dest string, amount float64) error; ClassTransfer(ctx, amount float64, toPerp bool) error; SpotUSDC(ctx) (float64, error); SetLeverage(ctx, coin string, leverage int, isCross bool) error; Positions(ctx) (PositionsSnapshot, error) }`
  - `type Factory interface { Account(privKey *ecdsa.PrivateKey, accountAddr string) Account }`
  - `func NewRealFactory(baseURL string) Factory`
  - `FakeAccount`, `FakeFactory` (in `fake.go`) for tests.

- [ ] **Step 1: Write the failing test (fake behavior contract)**

Create `internal/hl/account_test.go`:
```go
package hl

import (
	"context"
	"testing"
)

func TestFakeAccountRecordsCalls(t *testing.T) {
	f := &FakeAccount{Addr: "0xUSER", SpotBal: 5}
	ctx := context.Background()

	if err := f.SendUSD(ctx, "0xDEST", 100); err != nil {
		t.Fatal(err)
	}
	if len(f.SentUSD) != 1 || f.SentUSD[0].Amount != 100 || f.SentUSD[0].Dest != "0xDEST" {
		t.Fatalf("SendUSD not recorded: %+v", f.SentUSD)
	}

	_, err := f.PlaceOrder(ctx, OrderReq{Coin: "BTC", Market: "perp", IsBuy: true, OrderType: "market", Size: 0.01})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Orders) != 1 {
		t.Fatalf("order not recorded")
	}

	bal, _ := f.SpotUSDC(ctx)
	if bal != 5 {
		t.Fatalf("spot bal = %v", bal)
	}
}

func TestFakeFactoryReturnsConfiguredAccount(t *testing.T) {
	fa := &FakeAccount{Addr: "0xABC"}
	ff := &FakeFactory{ByAddr: map[string]Account{"0xABC": fa}}
	got := ff.Account(nil, "0xABC")
	if got.Address() != "0xABC" {
		t.Fatalf("got %s", got.Address())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/snojj25/Desktop/505/Flare/tee/extension-examples/hype-accounts && go test ./internal/hl/ -v`
Expected: FAIL — undefined types.

- [ ] **Step 3: Implement the interfaces + real impl**

Create `internal/hl/account.go`:
```go
// Package hl adapts github.com/sonirico/go-hyperliquid to a small Account
// interface so the extension's handlers can be unit-tested with fakes.
package hl

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"strconv"

	gohl "github.com/sonirico/go-hyperliquid"
)

type OrderReq struct {
	Coin       string
	Market     string // "perp" | "spot"
	IsBuy      bool
	OrderType  string // "market" | "limit"
	Size       float64
	LimitPx    float64
	ReduceOnly bool
	Leverage   int
	IsCross    bool
}

type OrderResult struct {
	Raw     json.RawMessage
	OrderID int64
}

type Position struct {
	Coin          string  `json:"coin"`
	Szi           float64 `json:"szi"`
	EntryPx       float64 `json:"entryPx"`
	UnrealizedPnl float64 `json:"unrealizedPnl"`
}

type PositionsSnapshot struct {
	Perp                []Position `json:"perp"`
	PerpAccountValueUSD float64    `json:"perpAccountValueUsd"`
	SpotUSDC            float64    `json:"spotUsdc"`
}

type Account interface {
	Address() string
	PlaceOrder(ctx context.Context, req OrderReq) (OrderResult, error)
	Cancel(ctx context.Context, coin string, oid int64) error
	SendUSD(ctx context.Context, dest string, amount float64) error
	ClassTransfer(ctx context.Context, amount float64, toPerp bool) error
	SpotUSDC(ctx context.Context) (float64, error)
	SetLeverage(ctx context.Context, coin string, leverage int, isCross bool) error
	Positions(ctx context.Context) (PositionsSnapshot, error)
}

type Factory interface {
	Account(privKey *ecdsa.PrivateKey, accountAddr string) Account
}

type realFactory struct{ baseURL string }

func NewRealFactory(baseURL string) Factory { return &realFactory{baseURL: baseURL} }

func (f *realFactory) Account(privKey *ecdsa.PrivateKey, accountAddr string) Account {
	ex := gohl.NewExchange(context.Background(), privKey, f.baseURL, nil, "", accountAddr, nil, nil)
	return &realAccount{ex: ex, addr: accountAddr}
}

type realAccount struct {
	ex   *gohl.Exchange
	addr string
}

func (a *realAccount) Address() string { return a.addr }

func (a *realAccount) PlaceOrder(ctx context.Context, req OrderReq) (OrderResult, error) {
	var (
		status gohl.OrderStatus
		err    error
	)
	if req.OrderType == "market" {
		status, err = a.ex.MarketOpen(ctx, req.Coin, req.IsBuy, req.Size, nil, 0.05, nil, nil)
	} else {
		status, err = a.ex.Order(ctx, gohl.CreateOrderRequest{
			Coin:       req.Coin,
			IsBuy:      req.IsBuy,
			Price:      req.LimitPx,
			Size:       req.Size,
			ReduceOnly: req.ReduceOnly,
			OrderType:  gohl.OrderType{Limit: &gohl.LimitOrderType{Tif: gohl.TifGtc}},
		}, nil)
	}
	if err != nil {
		return OrderResult{}, err
	}
	raw, _ := json.Marshal(status)
	res := OrderResult{Raw: raw}
	if status.Resting != nil {
		res.OrderID = status.Resting.OrderID
	}
	return res, nil
}

func (a *realAccount) Cancel(ctx context.Context, coin string, oid int64) error {
	_, err := a.ex.BulkCancel(ctx, []gohl.CancelOrderRequest{{Coin: coin, OrderID: oid}})
	return err
}

func (a *realAccount) SendUSD(ctx context.Context, dest string, amount float64) error {
	_, err := a.ex.UsdTransfer(ctx, amount, dest)
	return err
}

func (a *realAccount) ClassTransfer(ctx context.Context, amount float64, toPerp bool) error {
	_, err := a.ex.UsdClassTransfer(ctx, amount, toPerp)
	return err
}

func (a *realAccount) SetLeverage(ctx context.Context, coin string, leverage int, isCross bool) error {
	_, err := a.ex.UpdateLeverage(ctx, leverage, coin, isCross)
	return err
}

func (a *realAccount) SpotUSDC(ctx context.Context) (float64, error) {
	st, err := a.ex.Info().SpotUserState(ctx, a.addr)
	if err != nil {
		return 0, err
	}
	for _, b := range st.Balances {
		if b.Coin == "USDC" {
			return strconv.ParseFloat(b.Total, 64)
		}
	}
	return 0, nil
}

func (a *realAccount) Positions(ctx context.Context) (PositionsSnapshot, error) {
	var snap PositionsSnapshot
	us, err := a.ex.Info().UserState(ctx, a.addr)
	if err != nil {
		return snap, err
	}
	snap.PerpAccountValueUSD, _ = strconv.ParseFloat(us.MarginSummary.AccountValue, 64)
	for _, ap := range us.AssetPositions {
		szi, _ := strconv.ParseFloat(ap.Position.Szi, 64)
		entry, _ := strconv.ParseFloat(ap.Position.EntryPx, 64)
		pnl, _ := strconv.ParseFloat(ap.Position.UnrealizedPnl, 64)
		snap.Perp = append(snap.Perp, Position{Coin: ap.Position.Coin, Szi: szi, EntryPx: entry, UnrealizedPnl: pnl})
	}
	if spot, err := a.SpotUSDC(ctx); err == nil {
		snap.SpotUSDC = spot
	}
	return snap, nil
}

// compile-time check
var _ = fmt.Sprintf
```

> **Implementer note:** the exact field names on `gohl.OrderStatus`, `us.MarginSummary.AccountValue`, `ap.Position.*`, and `SpotUserState().Balances[].{Coin,Total}` must be confirmed against `go-hyperliquid@v0.33.1` source (`types.go`, `info.go`). If a field differs, adjust the parse — these are the only network-coupled lines. Verify with `go build ./internal/hl/`.

- [ ] **Step 4: Implement the fakes**

Create `internal/hl/fake.go`:
```go
package hl

import "context"

type USDSend struct {
	Dest   string
	Amount float64
}

type FakeAccount struct {
	Addr        string
	SpotBal     float64
	PosSnapshot PositionsSnapshot
	OrderErr    error

	SentUSD   []USDSend
	Orders    []OrderReq
	Cancels   []int64
	Transfers []float64
	Leverages []int
}

func (f *FakeAccount) Address() string { return f.Addr }

func (f *FakeAccount) PlaceOrder(_ context.Context, req OrderReq) (OrderResult, error) {
	if f.OrderErr != nil {
		return OrderResult{}, f.OrderErr
	}
	f.Orders = append(f.Orders, req)
	return OrderResult{OrderID: 12345}, nil
}

func (f *FakeAccount) Cancel(_ context.Context, _ string, oid int64) error {
	f.Cancels = append(f.Cancels, oid)
	return nil
}

func (f *FakeAccount) SendUSD(_ context.Context, dest string, amount float64) error {
	f.SentUSD = append(f.SentUSD, USDSend{Dest: dest, Amount: amount})
	return nil
}

func (f *FakeAccount) ClassTransfer(_ context.Context, amount float64, _ bool) error {
	f.Transfers = append(f.Transfers, amount)
	return nil
}

func (f *FakeAccount) SpotUSDC(_ context.Context) (float64, error) { return f.SpotBal, nil }

func (f *FakeAccount) SetLeverage(_ context.Context, _ string, lev int, _ bool) error {
	f.Leverages = append(f.Leverages, lev)
	return nil
}

func (f *FakeAccount) Positions(_ context.Context) (PositionsSnapshot, error) {
	return f.PosSnapshot, nil
}

type FakeFactory struct {
	ByAddr map[string]Account
	Last   Account
}

func (f *FakeFactory) Account(_ *ecdsaPriv, accountAddr string) Account { // see note
	if a, ok := f.ByAddr[accountAddr]; ok {
		f.Last = a
		return a
	}
	a := &FakeAccount{Addr: accountAddr}
	if f.ByAddr == nil {
		f.ByAddr = map[string]Account{}
	}
	f.ByAddr[accountAddr] = a
	f.Last = a
	return a
}
```

> **Implementer note:** replace the `*ecdsaPriv` placeholder with `*ecdsa.PrivateKey` and add `import "crypto/ecdsa"`. (Written this way only to flag that `FakeFactory.Account` must match the `Factory` interface signature exactly.)

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd /Users/snojj25/Desktop/505/Flare/tee/extension-examples/hype-accounts && go test ./internal/hl/ -v && go build ./internal/hl/`
Expected: PASS (2 tests) and the real adapter compiles.

- [ ] **Step 6: Commit**

```bash
cd /Users/snojj25/Desktop/505/Flare/tee/extension-examples/hype-accounts && git add internal/hl/ && git commit -m "feat: HL account adapter, factory, and test fakes"
```

---

### Task 6: Types, envelope decoder, and decoder registrations

**Files:**
- Modify: `pkg/types/types.go`
- Modify: `pkg/types/register.go`
- Test: `pkg/types/types_test.go` (create)

**Interfaces:**
- Produces:
  - `func DecodeEnvelope(message []byte) (common.Address, []byte, error)` — splits `abi.encode(address,bytes)`.
  - request structs: `DepositRequest{UsdAmount float64}`, `PlaceOrderRequest{Market,Coin,IsBuy,OrderType,Size,LimitPx,ReduceOnly,Leverage,IsCross}`, `CancelRequest{Coin,OrderID}`, `WithdrawRequest{UsdAmount}`.
  - response structs: `DepositResponse{HLAddress,BalanceUsd}`, `OrderResponse{Accepted bool, OrderID int64, Detail json.RawMessage}`, `CancelResponse{Cancelled bool}`, `WithdrawResponse{BalanceUsd float64}`.
  - `State{Accounts []AccountView}` + `AccountView{FlareAddress,HLAddress,BalanceUsd,RecentOrders}` and the existing `StateResponse` envelope (unchanged).

- [ ] **Step 1: Write the failing test**

Create `pkg/types/types_test.go`:
```go
package types

import (
	"testing"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
)

func TestDecodeEnvelope(t *testing.T) {
	addrTy, _ := abi.NewType("address", "", nil)
	bytesTy, _ := abi.NewType("bytes", "", nil)
	args := abi.Arguments{{Type: addrTy}, {Type: bytesTy}}

	user := common.HexToAddress("0x1234567890123456789012345678901234567890")
	payload := []byte(`{"usdAmount":100}`)
	packed, err := args.Pack(user, payload)
	if err != nil {
		t.Fatal(err)
	}

	gotAddr, gotPayload, err := DecodeEnvelope(packed)
	if err != nil {
		t.Fatal(err)
	}
	if gotAddr != user {
		t.Fatalf("addr = %s", gotAddr.Hex())
	}
	if string(gotPayload) != string(payload) {
		t.Fatalf("payload = %s", gotPayload)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/snojj25/Desktop/505/Flare/tee/extension-examples/hype-accounts && go test ./pkg/types/ -run TestDecodeEnvelope -v`
Expected: FAIL — `DecodeEnvelope` undefined.

- [ ] **Step 3: Replace types.go content**

Rewrite `pkg/types/types.go` (drop the greeting types):
```go
// Package types contains request/response types and the instruction envelope.
package types

import (
	"encoding/json"
	"fmt"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
)

// envelopeArgs describes abi.encode(address user, bytes payload).
var envelopeArgs abi.Arguments

func init() {
	addrTy, _ := abi.NewType("address", "", nil)
	bytesTy, _ := abi.NewType("bytes", "", nil)
	envelopeArgs = abi.Arguments{{Type: addrTy}, {Type: bytesTy}}
}

// DecodeEnvelope splits the on-chain instruction message into the calling
// user's address and the JSON payload.
func DecodeEnvelope(message []byte) (common.Address, []byte, error) {
	vals, err := envelopeArgs.Unpack(message)
	if err != nil {
		return common.Address{}, nil, fmt.Errorf("unpack envelope: %w", err)
	}
	if len(vals) != 2 {
		return common.Address{}, nil, fmt.Errorf("envelope: expected 2 fields, got %d", len(vals))
	}
	addr, ok := vals[0].(common.Address)
	if !ok {
		return common.Address{}, nil, fmt.Errorf("envelope: field 0 not address")
	}
	payload, ok := vals[1].([]byte)
	if !ok {
		return common.Address{}, nil, fmt.Errorf("envelope: field 1 not bytes")
	}
	return addr, payload, nil
}

// --- Requests (JSON payloads) ---

type DepositRequest struct {
	UsdAmount float64 `json:"usdAmount"`
}

type PlaceOrderRequest struct {
	Market     string  `json:"market"` // "perp" | "spot"
	Coin       string  `json:"coin"`
	IsBuy      bool    `json:"isBuy"`
	OrderType  string  `json:"orderType"` // "market" | "limit"
	Size       float64 `json:"size"`
	LimitPx    float64 `json:"limitPx"`
	ReduceOnly bool    `json:"reduceOnly"`
	Leverage   int     `json:"leverage"`
	IsCross    bool    `json:"isCross"`
}

type CancelRequest struct {
	Coin    string `json:"coin"`
	OrderID int64  `json:"orderId"`
}

type WithdrawRequest struct {
	UsdAmount float64 `json:"usdAmount"`
}

// --- Responses (ActionResult.Data) ---

type DepositResponse struct {
	HLAddress  string  `json:"hlAddress"`
	BalanceUsd float64 `json:"balanceUsd"`
}

type OrderResponse struct {
	Accepted bool            `json:"accepted"`
	OrderID  int64           `json:"orderId"`
	Detail   json.RawMessage `json:"detail,omitempty"`
}

type CancelResponse struct {
	Cancelled bool `json:"cancelled"`
}

type WithdrawResponse struct {
	BalanceUsd float64 `json:"balanceUsd"`
}

// --- State (GET /state) ---

type AccountView struct {
	FlareAddress string      `json:"flareAddress"`
	HLAddress    string      `json:"hlAddress"`
	BalanceUsd   float64     `json:"balanceUsd"`
	RecentOrders interface{} `json:"recentOrders"`
}

type State struct {
	Accounts []AccountView `json:"accounts"`
}

// --- DO NOT MODIFY below this line. ---

type StateResponse struct {
	StateVersion common.Hash `json:"stateVersion"`
	State        State       `json:"state"`
}
```

- [ ] **Step 4: Replace register.go content**

Rewrite `pkg/types/register.go`:
```go
package types

import "extension-scaffold/pkg/decoder"

// RegisterDecoders registers JSON decoders for HL command messages and results.
func RegisterDecoders(r *decoder.Registry) {
	type pair struct {
		cmd string
	}
	// messages
	r.Register(decoder.RegistryKey{OPType: "HL", OPCommand: "DEPOSIT", Kind: decoder.KindMessage}, decoder.NewJSONDecoder[DepositRequest]())
	r.Register(decoder.RegistryKey{OPType: "HL", OPCommand: "PLACE_ORDER", Kind: decoder.KindMessage}, decoder.NewJSONDecoder[PlaceOrderRequest]())
	r.Register(decoder.RegistryKey{OPType: "HL", OPCommand: "CANCEL", Kind: decoder.KindMessage}, decoder.NewJSONDecoder[CancelRequest]())
	r.Register(decoder.RegistryKey{OPType: "HL", OPCommand: "WITHDRAW", Kind: decoder.KindMessage}, decoder.NewJSONDecoder[WithdrawRequest]())
	// results
	r.Register(decoder.RegistryKey{OPType: "HL", OPCommand: "DEPOSIT", Kind: decoder.KindResult}, decoder.NewJSONDecoder[DepositResponse]())
	r.Register(decoder.RegistryKey{OPType: "HL", OPCommand: "PLACE_ORDER", Kind: decoder.KindResult}, decoder.NewJSONDecoder[OrderResponse]())
	r.Register(decoder.RegistryKey{OPType: "HL", OPCommand: "CANCEL", Kind: decoder.KindResult}, decoder.NewJSONDecoder[CancelResponse]())
	r.Register(decoder.RegistryKey{OPType: "HL", OPCommand: "WITHDRAW", Kind: decoder.KindResult}, decoder.NewJSONDecoder[WithdrawResponse]())
	_ = pair{}
}
```
> **Implementer note:** remove the unused `pair` helper if `go vet` flags it; it is only there to avoid an empty-import edge case. Confirm the message decoder registration matches the envelope: the types-server decodes the *inner* JSON payload, so registering the request structs as JSON is correct, but the envelope's `(address,bytes)` wrapper means the types-server sees the wrapper. If the types-server must decode the wrapper, register an ABI decoder for the envelope instead — verify against `docs/types-server.md` during implementation and adjust. This does not affect the TEE runtime path (the extension calls `DecodeEnvelope` directly).

- [ ] **Step 5: Run test to verify it passes**

Run: `cd /Users/snojj25/Desktop/505/Flare/tee/extension-examples/hype-accounts && go test ./pkg/types/ -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
cd /Users/snojj25/Desktop/505/Flare/tee/extension-examples/hype-accounts && git add pkg/types/ && git commit -m "feat: HL request/response types, envelope decoder, registrations"
```

---

### Task 7: Extension struct, router, state, and async plumbing

**Files:**
- Modify: `internal/extension/extension.go`
- Modify: `internal/extension/utils.go`
- Test: `internal/extension/router_test.go` (create)

**Interfaces:**
- Consumes: `config.*`, `keys.DeriveUserKey`, `ledger.*`, `hl.Factory/Account`, `types.DecodeEnvelope`.
- Produces: `Extension` struct with fields `factory hl.Factory`, `treasury hl.Account`, `treasuryAddr string`, `masterSeed []byte`, `led *ledger.Ledger`, `accounts map[common.Address]hl.Account`, `signPort int`; `func New(extensionPort, signPort int) *Extension`; `func NewWith(extensionPort, signPort int, f hl.Factory, treasury hl.Account, treasuryAddr string, seed []byte) *Extension` (test constructor); helper `accountFor(user common.Address) (hl.Account, error)`; async helper `handleAsync(action, df, processor)`.

- [ ] **Step 1: Write the failing test**

Create `internal/extension/router_test.go`:
```go
package extension

import (
	"bytes"
	"testing"

	"extension-scaffold/internal/hl"

	"github.com/ethereum/go-ethereum/common"
)

func newTestExt() *Extension {
	seed := bytes.Repeat([]byte{0x01}, 32)
	treasury := &hl.FakeAccount{Addr: "0xTREASURY"}
	return NewWith(0, 0, &hl.FakeFactory{}, treasury, "0xTREASURY", seed)
}

func TestAccountForIsDeterministicAndCached(t *testing.T) {
	e := newTestExt()
	u := common.HexToAddress("0x1111111111111111111111111111111111111111")
	a1, err := e.accountFor(u)
	if err != nil {
		t.Fatal(err)
	}
	a2, _ := e.accountFor(u)
	if a1 != a2 {
		t.Fatal("expected cached account instance")
	}
	if a1.Address() == "" {
		t.Fatal("expected derived HL address")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/snojj25/Desktop/505/Flare/tee/extension-examples/hype-accounts && go test ./internal/extension/ -run TestAccountFor -v`
Expected: FAIL — `NewWith`, `accountFor` undefined / build errors from old greeting code.

- [ ] **Step 3: Rewrite extension.go**

Replace `internal/extension/extension.go` with:
```go
package extension

import (
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"extension-scaffold/internal/config"
	"extension-scaffold/internal/hl"
	"extension-scaffold/internal/keys"
	"extension-scaffold/internal/ledger"
	"extension-scaffold/pkg/types"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/flare-foundation/go-flare-common/pkg/tee/instruction"
	teetypes "github.com/flare-foundation/tee-node/pkg/types"
	teeutils "github.com/flare-foundation/tee-node/pkg/utils"

	"github.com/flare-foundation/tee-node/pkg/processorutils"
)

type Extension struct {
	mu       sync.RWMutex
	Server   *http.Server
	signPort int

	factory      hl.Factory
	treasury     hl.Account
	treasuryAddr string
	masterSeed   []byte
	led          *ledger.Ledger

	accounts map[common.Address]hl.Account // cache: flare addr -> HL account
}

// New is the production constructor. It loads HL config and builds the
// treasury account from the configured key.
func New(extensionPort, signPort int) *Extension {
	if err := config.LoadHLConfig(); err != nil {
		panic(fmt.Sprintf("HL config: %v", err))
	}
	seed, err := keys.MustHexSeed(config.MasterSeedHex)
	if err != nil {
		panic(fmt.Sprintf("master seed: %v", err))
	}
	treasuryKey, err := crypto.HexToECDSA(config.TreasuryKeyHex)
	if err != nil {
		panic(fmt.Sprintf("treasury key: %v", err))
	}
	treasuryAddr := crypto.PubkeyToAddress(treasuryKey.PublicKey).Hex()
	factory := hl.NewRealFactory(config.HyperliquidURL)
	treasury := factory.Account(treasuryKey, treasuryAddr)

	return buildExtension(extensionPort, signPort, factory, treasury, treasuryAddr, seed)
}

// NewWith is the test constructor with injected dependencies.
func NewWith(extensionPort, signPort int, f hl.Factory, treasury hl.Account, treasuryAddr string, seed []byte) *Extension {
	return buildExtension(extensionPort, signPort, f, treasury, treasuryAddr, seed)
}

func buildExtension(extensionPort, signPort int, f hl.Factory, treasury hl.Account, treasuryAddr string, seed []byte) *Extension {
	e := &Extension{
		signPort:     signPort,
		factory:      f,
		treasury:     treasury,
		treasuryAddr: treasuryAddr,
		masterSeed:   seed,
		led:          ledger.New(),
		accounts:     map[common.Address]hl.Account{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /state", e.stateHandler)
	mux.HandleFunc("POST /action", e.actionHandler)
	e.Server = &http.Server{Addr: fmt.Sprintf(":%d", extensionPort), Handler: mux}
	return e
}

// accountFor returns the cached (or newly derived) HL account for a Flare user.
func (e *Extension) accountFor(user common.Address) (hl.Account, error) {
	e.mu.RLock()
	a, ok := e.accounts[user]
	e.mu.RUnlock()
	if ok {
		return a, nil
	}
	var priv *ecdsa.PrivateKey
	priv, err := keys.DeriveUserKey(e.masterSeed, user)
	if err != nil {
		return nil, err
	}
	addr := crypto.PubkeyToAddress(priv.PublicKey).Hex()
	acct := e.factory.Account(priv, addr)

	e.mu.Lock()
	e.accounts[user] = acct
	e.mu.Unlock()
	return acct, nil
}

func (e *Extension) stateHandler(w http.ResponseWriter, r *http.Request) {
	e.mu.RLock()
	views := make([]types.AccountView, 0, len(e.accounts))
	for flareAddr, acct := range e.accounts {
		snap := e.led.SnapshotFor(flareAddr)
		views = append(views, types.AccountView{
			FlareAddress: flareAddr.Hex(),
			HLAddress:    acct.Address(),
			BalanceUsd:   snap.BalanceUSD,
			RecentOrders: snap.RecentOrders,
		})
	}
	e.mu.RUnlock()

	resp := types.StateResponse{
		StateVersion: teeutils.ToHash(config.Version),
		State:        types.State{Accounts: views},
	}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		http.Error(w, fmt.Sprintf("sending response: %v", err), http.StatusInternalServerError)
	}
}

func (e *Extension) processAction(action teetypes.Action) (int, []byte) {
	df, err := processorutils.Parse[instruction.DataFixed](action.Data.Message)
	if err != nil {
		return http.StatusBadRequest, []byte(fmt.Sprintf("decoding fixed data: %v", err))
	}
	if df.OPType != teeutils.ToHash(config.OPTypeHL) {
		return http.StatusNotImplemented, []byte(fmt.Sprintf("unsupported op type: %s", df.OPType.Hex()))
	}
	switch {
	case df.OPCommand == teeutils.ToHash(config.OPCommandDeposit):
		return e.handleAsync(action, df, e.processDeposit)
	case df.OPCommand == teeutils.ToHash(config.OPCommandPlaceOrder):
		return e.handleAsync(action, df, e.processPlaceOrder)
	case df.OPCommand == teeutils.ToHash(config.OPCommandCancel):
		return e.handleAsync(action, df, e.processCancel)
	case df.OPCommand == teeutils.ToHash(config.OPCommandWithdraw):
		return e.handleAsync(action, df, e.processWithdraw)
	default:
		return http.StatusNotImplemented, []byte(fmt.Sprintf("unsupported op command: %s", df.OPCommand.Hex()))
	}
}
```

- [ ] **Step 4: Add async helper to utils.go**

Append to `internal/extension/utils.go` (keep `actionHandler`, `buildResult`, `getLogFromBody`):
```go
import additions: "bytes", "io", "time", teetypes already imported, add:
	"github.com/flare-foundation/go-flare-common/pkg/logger"  // already imported
```
Add functions:
```go
type processorFunc func(teetypes.Action, *instruction.DataFixed) teetypes.ActionResult

func (e *Extension) handleAsync(action teetypes.Action, df *instruction.DataFixed, processor processorFunc) (int, []byte) {
	ar := teetypes.ActionResult{
		ID:            action.Data.ID,
		SubmissionTag: action.Data.SubmissionTag,
		Version:       config.Version,
		OPType:        df.OPType,
		OPCommand:     df.OPCommand,
		Status:        2,
		Log:           "pending",
	}
	pending, _ := json.Marshal(ar)

	go func(a teetypes.Action, d *instruction.DataFixed) {
		result := processor(a, d)
		if err := sendResult(e.signPort, result); err != nil {
			logger.Errorf("posting result %s: %v", result.ID, err)
		}
	}(action, df)

	return http.StatusOK, pending
}

func sendResult(nodePort int, ar teetypes.ActionResult) error {
	url := fmt.Sprintf("http://localhost:%d/result", nodePort)
	b, _ := json.Marshal(ar)
	resp, err := http.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("node /result returned %d: %s", resp.StatusCode, string(body))
	}
	return nil
}
```
Add imports `"bytes"`, `"io"`, `"extension-scaffold/internal/config"`, `"github.com/flare-foundation/go-flare-common/pkg/tee/instruction"` to `utils.go` as needed (config + instruction may already be present via the package).

- [ ] **Step 5: Delete the old greeting handlers**

Remove `processGreeting`, `processSayHello`, `processSayGoodbye` (they live in the old `extension.go`, now replaced). The four `processDeposit/processPlaceOrder/processCancel/processWithdraw` referenced by the router are added in Tasks 8–11; to keep this task compiling, add temporary stubs in a new `internal/extension/processors.go`:
```go
package extension

import (
	teetypes "github.com/flare-foundation/tee-node/pkg/types"
	"github.com/flare-foundation/go-flare-common/pkg/tee/instruction"
)

func (e *Extension) processDeposit(a teetypes.Action, df *instruction.DataFixed) teetypes.ActionResult {
	return buildResult(a, df, nil, 0, errNotImplemented)
}
func (e *Extension) processPlaceOrder(a teetypes.Action, df *instruction.DataFixed) teetypes.ActionResult {
	return buildResult(a, df, nil, 0, errNotImplemented)
}
func (e *Extension) processCancel(a teetypes.Action, df *instruction.DataFixed) teetypes.ActionResult {
	return buildResult(a, df, nil, 0, errNotImplemented)
}
func (e *Extension) processWithdraw(a teetypes.Action, df *instruction.DataFixed) teetypes.ActionResult {
	return buildResult(a, df, nil, 0, errNotImplemented)
}

var errNotImplemented = fmt.Errorf("not implemented")
```
Add `"fmt"` import.

- [ ] **Step 6: Run test to verify it passes**

Run: `cd /Users/snojj25/Desktop/505/Flare/tee/extension-examples/hype-accounts && go test ./internal/extension/ -run TestAccountFor -v && go build ./...`
Expected: PASS; full module builds.

- [ ] **Step 7: Commit**

```bash
cd /Users/snojj25/Desktop/505/Flare/tee/extension-examples/hype-accounts && git add internal/extension/ && git commit -m "feat: HL extension struct, router, state, async plumbing"
```

---

### Task 8: DEPOSIT handler

**Files:**
- Modify: `internal/extension/processors.go`
- Test: `internal/extension/processors_test.go` (create)

**Interfaces:**
- Consumes: `types.DecodeEnvelope`, `types.DepositRequest`, `e.treasury.SendUSD`, `e.led.Credit`, `config.MaxDepositUSD`, `config.MaxTotalDepositUSD`.
- Produces: `processDeposit` returns `types.DepositResponse` JSON in `Data` with status `1`.

- [ ] **Step 1: Write the failing test**

Create `internal/extension/processors_test.go`:
```go
package extension

import (
	"bytes"
	"encoding/json"
	"testing"

	"extension-scaffold/internal/hl"
	"extension-scaffold/pkg/types"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/flare-foundation/go-flare-common/pkg/tee/instruction"
	teetypes "github.com/flare-foundation/tee-node/pkg/types"
)

func envelope(t *testing.T, user common.Address, payload any) []byte {
	t.Helper()
	addrTy, _ := abi.NewType("address", "", nil)
	bytesTy, _ := abi.NewType("bytes", "", nil)
	args := abi.Arguments{{Type: addrTy}, {Type: bytesTy}}
	pj, _ := json.Marshal(payload)
	packed, err := args.Pack(user, pj)
	if err != nil {
		t.Fatal(err)
	}
	return packed
}

func actionFor(msg []byte) (teetypes.Action, *instruction.DataFixed) {
	df := &instruction.DataFixed{OriginalMessage: msg}
	return teetypes.Action{}, df
}

func TestProcessDepositFundsAndCredits(t *testing.T) {
	seed := bytes.Repeat([]byte{0x01}, 32)
	treasury := &hl.FakeAccount{Addr: "0xTREASURY"}
	e := NewWith(0, 0, &hl.FakeFactory{}, treasury, "0xTREASURY", seed)

	user := common.HexToAddress("0x1111111111111111111111111111111111111111")
	msg := envelope(t, user, types.DepositRequest{UsdAmount: 100})
	a, df := actionFor(msg)

	res := e.processDeposit(a, df)
	if res.Status != 1 {
		t.Fatalf("status = %d log = %s", res.Status, res.Log)
	}
	if len(treasury.SentUSD) != 1 || treasury.SentUSD[0].Amount != 100 {
		t.Fatalf("treasury transfer wrong: %+v", treasury.SentUSD)
	}
	if e.led.Balance(user) != 100 {
		t.Fatalf("ledger balance = %v", e.led.Balance(user))
	}
	var resp types.DepositResponse
	if err := json.Unmarshal(res.Data, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.BalanceUsd != 100 {
		t.Fatalf("resp balance = %v", resp.BalanceUsd)
	}
}

func TestProcessDepositRejectsOverCap(t *testing.T) {
	seed := bytes.Repeat([]byte{0x01}, 32)
	e := NewWith(0, 0, &hl.FakeFactory{}, &hl.FakeAccount{Addr: "0xT"}, "0xT", seed)
	user := common.HexToAddress("0x2222222222222222222222222222222222222222")
	msg := envelope(t, user, types.DepositRequest{UsdAmount: 1e9})
	a, df := actionFor(msg)
	res := e.processDeposit(a, df)
	if res.Status != 0 {
		t.Fatalf("expected rejection, got status %d", res.Status)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/snojj25/Desktop/505/Flare/tee/extension-examples/hype-accounts && go test ./internal/extension/ -run TestProcessDeposit -v`
Expected: FAIL — stub returns "not implemented".

- [ ] **Step 3: Implement processDeposit**

In `internal/extension/processors.go`, replace the `processDeposit` stub with:
```go
func (e *Extension) processDeposit(a teetypes.Action, df *instruction.DataFixed) teetypes.ActionResult {
	user, payload, err := types.DecodeEnvelope(df.OriginalMessage)
	if err != nil {
		return buildResult(a, df, nil, 0, err)
	}
	var req types.DepositRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return buildResult(a, df, nil, 0, fmt.Errorf("decoding deposit: %w", err))
	}
	if req.UsdAmount <= 0 {
		return buildResult(a, df, nil, 0, fmt.Errorf("usdAmount must be positive"))
	}
	if req.UsdAmount > config.MaxDepositUSD {
		return buildResult(a, df, nil, 0, fmt.Errorf("deposit %.2f exceeds per-deposit cap %.2f", req.UsdAmount, config.MaxDepositUSD))
	}
	if e.led.Total()+req.UsdAmount > config.MaxTotalDepositUSD {
		return buildResult(a, df, nil, 0, fmt.Errorf("deposit exceeds global treasury cap"))
	}

	acct, err := e.accountFor(user)
	if err != nil {
		return buildResult(a, df, nil, 0, fmt.Errorf("deriving account: %w", err))
	}
	if err := e.treasury.SendUSD(context.Background(), acct.Address(), req.UsdAmount); err != nil {
		return buildResult(a, df, nil, 0, fmt.Errorf("funding from treasury: %w", err))
	}
	e.led.Credit(user, req.UsdAmount)

	resp := types.DepositResponse{HLAddress: acct.Address(), BalanceUsd: e.led.Balance(user)}
	data, _ := json.Marshal(resp)
	return buildResult(a, df, data, 1, nil)
}
```
Update `processors.go` imports to: `"context"`, `"encoding/json"`, `"fmt"`, `"extension-scaffold/internal/config"`, `"extension-scaffold/pkg/types"`, `teetypes`, `instruction`. Remove `errNotImplemented` once all four stubs are replaced (keep it until Task 11).

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/snojj25/Desktop/505/Flare/tee/extension-examples/hype-accounts && go test ./internal/extension/ -run TestProcessDeposit -v`
Expected: PASS (2 tests).

- [ ] **Step 5: Commit**

```bash
cd /Users/snojj25/Desktop/505/Flare/tee/extension-examples/hype-accounts && git add internal/extension/ && git commit -m "feat: DEPOSIT handler funds per-user account from treasury"
```

---

### Task 9: PLACE_ORDER handler (spot + perp, market + limit)

**Files:**
- Modify: `internal/extension/processors.go`
- Test: `internal/extension/processors_test.go`

**Interfaces:**
- Consumes: `types.PlaceOrderRequest`, `hl.OrderReq`, `acct.SetLeverage`, `acct.SpotUSDC`, `acct.ClassTransfer`, `acct.PlaceOrder`, `e.led.AddOrder`.
- Produces: `processPlaceOrder` returns `types.OrderResponse` with status `1`.

- [ ] **Step 1: Write the failing tests**

Add to `internal/extension/processors_test.go`:
```go
func TestProcessPerpMarketOrderSetsLeverageAndPlaces(t *testing.T) {
	seed := bytes.Repeat([]byte{0x01}, 32)
	user := common.HexToAddress("0x3333333333333333333333333333333333333333")

	// Pre-derive the account so we can inspect the fake afterwards.
	e := NewWith(0, 0, &hl.FakeFactory{}, &hl.FakeAccount{Addr: "0xT"}, "0xT", seed)
	acct, _ := e.accountFor(user)
	fa := acct.(*hl.FakeAccount)

	msg := envelope(t, user, types.PlaceOrderRequest{
		Market: "perp", Coin: "BTC", IsBuy: true, OrderType: "market", Size: 0.01, Leverage: 5,
	})
	a, df := actionFor(msg)
	res := e.processPlaceOrder(a, df)
	if res.Status != 1 {
		t.Fatalf("status = %d log = %s", res.Status, res.Log)
	}
	if len(fa.Leverages) != 1 || fa.Leverages[0] != 5 {
		t.Fatalf("leverage not set: %+v", fa.Leverages)
	}
	if len(fa.Orders) != 1 || fa.Orders[0].Coin != "BTC" {
		t.Fatalf("order not placed: %+v", fa.Orders)
	}
}

func TestProcessSpotOrderClassTransfersWhenSpotLow(t *testing.T) {
	seed := bytes.Repeat([]byte{0x01}, 32)
	user := common.HexToAddress("0x4444444444444444444444444444444444444444")
	e := NewWith(0, 0, &hl.FakeFactory{}, &hl.FakeAccount{Addr: "0xT"}, "0xT", seed)
	acct, _ := e.accountFor(user)
	fa := acct.(*hl.FakeAccount)
	fa.SpotBal = 0 // forces a class transfer

	msg := envelope(t, user, types.PlaceOrderRequest{
		Market: "spot", Coin: "PURR/USDC", IsBuy: true, OrderType: "limit", Size: 10, LimitPx: 2,
	})
	a, df := actionFor(msg)
	res := e.processPlaceOrder(a, df)
	if res.Status != 1 {
		t.Fatalf("status = %d log = %s", res.Status, res.Log)
	}
	if len(fa.Transfers) != 1 {
		t.Fatalf("expected one class transfer, got %+v", fa.Transfers)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/snojj25/Desktop/505/Flare/tee/extension-examples/hype-accounts && go test ./internal/extension/ -run TestProcess.*Order -v`
Expected: FAIL — stub.

- [ ] **Step 3: Implement processPlaceOrder**

Replace the `processPlaceOrder` stub:
```go
func (e *Extension) processPlaceOrder(a teetypes.Action, df *instruction.DataFixed) teetypes.ActionResult {
	user, payload, err := types.DecodeEnvelope(df.OriginalMessage)
	if err != nil {
		return buildResult(a, df, nil, 0, err)
	}
	var req types.PlaceOrderRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return buildResult(a, df, nil, 0, fmt.Errorf("decoding order: %w", err))
	}
	if req.Coin == "" || req.Size <= 0 {
		return buildResult(a, df, nil, 0, fmt.Errorf("coin and positive size required"))
	}
	if req.Market != "perp" && req.Market != "spot" {
		return buildResult(a, df, nil, 0, fmt.Errorf("market must be perp or spot"))
	}
	if req.OrderType != "market" && req.OrderType != "limit" {
		return buildResult(a, df, nil, 0, fmt.Errorf("orderType must be market or limit"))
	}

	acct, err := e.accountFor(user)
	if err != nil {
		return buildResult(a, df, nil, 0, err)
	}
	ctx := context.Background()

	if req.Market == "perp" && req.Leverage > 0 {
		if err := acct.SetLeverage(ctx, req.Coin, req.Leverage, req.IsCross); err != nil {
			return buildResult(a, df, nil, 0, fmt.Errorf("set leverage: %w", err))
		}
	}

	if req.Market == "spot" && req.IsBuy {
		notional := req.Size * req.LimitPx
		if req.OrderType == "market" {
			notional = req.Size // px unknown; move size as a rough floor (see note)
		}
		spot, err := acct.SpotUSDC(ctx)
		if err != nil {
			return buildResult(a, df, nil, 0, fmt.Errorf("read spot balance: %w", err))
		}
		if spot < notional {
			if err := acct.ClassTransfer(ctx, notional-spot, false); err != nil {
				return buildResult(a, df, nil, 0, fmt.Errorf("perp->spot transfer: %w", err))
			}
		}
	}

	result, err := acct.PlaceOrder(ctx, hl.OrderReq{
		Coin: req.Coin, Market: req.Market, IsBuy: req.IsBuy,
		OrderType: req.OrderType, Size: req.Size, LimitPx: req.LimitPx,
		ReduceOnly: req.ReduceOnly, Leverage: req.Leverage, IsCross: req.IsCross,
	})
	if err != nil {
		return buildResult(a, df, nil, 0, fmt.Errorf("placing order: %w", err))
	}

	e.led.AddOrder(user, ledger.OrderRecord{
		InstructionID: df.InstructionID.Hex(), Coin: req.Coin, Market: req.Market,
		IsBuy: req.IsBuy, Size: req.Size, Status: "submitted",
	})

	resp := types.OrderResponse{Accepted: true, OrderID: result.OrderID, Detail: json.RawMessage(result.Raw)}
	data, _ := json.Marshal(resp)
	return buildResult(a, df, data, 1, nil)
}
```
Add `"extension-scaffold/internal/hl"` and `"extension-scaffold/internal/ledger"` to imports.
> **Implementer note:** the market-buy spot funding uses `Size` as a rough floor because the fill price is unknown pre-trade. This is the §10 "spot funding is approximate" simplification — acceptable for v1; a follow-up can fetch the mid price. Document this in the order handler comment.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/snojj25/Desktop/505/Flare/tee/extension-examples/hype-accounts && go test ./internal/extension/ -run TestProcess.*Order -v`
Expected: PASS (2 tests).

- [ ] **Step 5: Commit**

```bash
cd /Users/snojj25/Desktop/505/Flare/tee/extension-examples/hype-accounts && git add internal/extension/ && git commit -m "feat: PLACE_ORDER handler for spot and perp, market and limit"
```

---

### Task 10: CANCEL handler

**Files:**
- Modify: `internal/extension/processors.go`
- Test: `internal/extension/processors_test.go`

**Interfaces:**
- Consumes: `types.CancelRequest`, `acct.Cancel`.
- Produces: `processCancel` returns `types.CancelResponse{Cancelled:true}` status `1`.

- [ ] **Step 1: Write the failing test**

Add:
```go
func TestProcessCancel(t *testing.T) {
	seed := bytes.Repeat([]byte{0x01}, 32)
	user := common.HexToAddress("0x5555555555555555555555555555555555555555")
	e := NewWith(0, 0, &hl.FakeFactory{}, &hl.FakeAccount{Addr: "0xT"}, "0xT", seed)
	acct, _ := e.accountFor(user)
	fa := acct.(*hl.FakeAccount)

	msg := envelope(t, user, types.CancelRequest{Coin: "BTC", OrderID: 999})
	a, df := actionFor(msg)
	res := e.processCancel(a, df)
	if res.Status != 1 {
		t.Fatalf("status = %d", res.Status)
	}
	if len(fa.Cancels) != 1 || fa.Cancels[0] != 999 {
		t.Fatalf("cancel not recorded: %+v", fa.Cancels)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/snojj25/Desktop/505/Flare/tee/extension-examples/hype-accounts && go test ./internal/extension/ -run TestProcessCancel -v`
Expected: FAIL — stub.

- [ ] **Step 3: Implement processCancel**

Replace the stub:
```go
func (e *Extension) processCancel(a teetypes.Action, df *instruction.DataFixed) teetypes.ActionResult {
	user, payload, err := types.DecodeEnvelope(df.OriginalMessage)
	if err != nil {
		return buildResult(a, df, nil, 0, err)
	}
	var req types.CancelRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return buildResult(a, df, nil, 0, fmt.Errorf("decoding cancel: %w", err))
	}
	if req.Coin == "" || req.OrderID == 0 {
		return buildResult(a, df, nil, 0, fmt.Errorf("coin and orderId required"))
	}
	acct, err := e.accountFor(user)
	if err != nil {
		return buildResult(a, df, nil, 0, err)
	}
	if err := acct.Cancel(context.Background(), req.Coin, req.OrderID); err != nil {
		return buildResult(a, df, nil, 0, fmt.Errorf("cancelling: %w", err))
	}
	data, _ := json.Marshal(types.CancelResponse{Cancelled: true})
	return buildResult(a, df, data, 1, nil)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/snojj25/Desktop/505/Flare/tee/extension-examples/hype-accounts && go test ./internal/extension/ -run TestProcessCancel -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd /Users/snojj25/Desktop/505/Flare/tee/extension-examples/hype-accounts && git add internal/extension/ && git commit -m "feat: CANCEL handler"
```

---

### Task 11: WITHDRAW handler (internal stub)

**Files:**
- Modify: `internal/extension/processors.go`
- Test: `internal/extension/processors_test.go`

**Interfaces:**
- Consumes: `types.WithdrawRequest`, `e.led.Debit`, `acct.SendUSD` (→ treasury).
- Produces: `processWithdraw` returns `types.WithdrawResponse{BalanceUsd}` status `1`. Remove `errNotImplemented` after this task.

- [ ] **Step 1: Write the failing tests**

Add:
```go
func TestProcessWithdrawDebitsAndReturnsToTreasury(t *testing.T) {
	seed := bytes.Repeat([]byte{0x01}, 32)
	user := common.HexToAddress("0x6666666666666666666666666666666666666666")
	e := NewWith(0, 0, &hl.FakeFactory{}, &hl.FakeAccount{Addr: "0xTREASURY"}, "0xTREASURY", seed)
	acct, _ := e.accountFor(user)
	fa := acct.(*hl.FakeAccount)
	e.led.Credit(user, 200)

	msg := envelope(t, user, types.WithdrawRequest{UsdAmount: 80})
	a, df := actionFor(msg)
	res := e.processWithdraw(a, df)
	if res.Status != 1 {
		t.Fatalf("status = %d log = %s", res.Status, res.Log)
	}
	if e.led.Balance(user) != 120 {
		t.Fatalf("balance = %v", e.led.Balance(user))
	}
	if len(fa.SentUSD) != 1 || fa.SentUSD[0].Dest != "0xTREASURY" || fa.SentUSD[0].Amount != 80 {
		t.Fatalf("treasury return wrong: %+v", fa.SentUSD)
	}
}

func TestProcessWithdrawRejectsOverBalance(t *testing.T) {
	seed := bytes.Repeat([]byte{0x01}, 32)
	user := common.HexToAddress("0x7777777777777777777777777777777777777777")
	e := NewWith(0, 0, &hl.FakeFactory{}, &hl.FakeAccount{Addr: "0xTREASURY"}, "0xTREASURY", seed)
	e.led.Credit(user, 10)
	msg := envelope(t, user, types.WithdrawRequest{UsdAmount: 50})
	a, df := actionFor(msg)
	res := e.processWithdraw(a, df)
	if res.Status != 0 {
		t.Fatalf("expected rejection, got %d", res.Status)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/snojj25/Desktop/505/Flare/tee/extension-examples/hype-accounts && go test ./internal/extension/ -run TestProcessWithdraw -v`
Expected: FAIL — stub.

- [ ] **Step 3: Implement processWithdraw and remove the stub var**

Replace the stub and delete `var errNotImplemented`:
```go
func (e *Extension) processWithdraw(a teetypes.Action, df *instruction.DataFixed) teetypes.ActionResult {
	user, payload, err := types.DecodeEnvelope(df.OriginalMessage)
	if err != nil {
		return buildResult(a, df, nil, 0, err)
	}
	var req types.WithdrawRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return buildResult(a, df, nil, 0, fmt.Errorf("decoding withdraw: %w", err))
	}
	if req.UsdAmount <= 0 {
		return buildResult(a, df, nil, 0, fmt.Errorf("usdAmount must be positive"))
	}
	if err := e.led.Debit(user, req.UsdAmount); err != nil {
		return buildResult(a, df, nil, 0, err)
	}
	acct, err := e.accountFor(user)
	if err != nil {
		// refund the ledger debit on derivation failure
		e.led.Credit(user, req.UsdAmount)
		return buildResult(a, df, nil, 0, err)
	}
	if err := acct.SendUSD(context.Background(), e.treasuryAddr, req.UsdAmount); err != nil {
		e.led.Credit(user, req.UsdAmount) // refund on HL failure
		return buildResult(a, df, nil, 0, fmt.Errorf("returning to treasury: %w", err))
	}
	data, _ := json.Marshal(types.WithdrawResponse{BalanceUsd: e.led.Balance(user)})
	return buildResult(a, df, data, 1, nil)
}
```

- [ ] **Step 4: Run all extension tests**

Run: `cd /Users/snojj25/Desktop/505/Flare/tee/extension-examples/hype-accounts && go test ./internal/... -v && go build ./...`
Expected: PASS across config/keys/ledger/hl/extension; module builds.

- [ ] **Step 5: Commit**

```bash
cd /Users/snojj25/Desktop/505/Flare/tee/extension-examples/hype-accounts && git add internal/extension/ && git commit -m "feat: WITHDRAW handler returns funds to treasury (internal stub)"
```

---

### Task 12: Solidity contract — OP constants and four send functions

**Files:**
- Modify: `contracts/InstructionSender.sol`
- Then regenerate bindings.

**Interfaces:**
- Produces: contract emits `message = abi.encode(msg.sender, _payload)` under OPType `"HL"` with OPCommands `"DEPOSIT"|"PLACE_ORDER"|"CANCEL"|"WITHDRAW"`.

- [ ] **Step 1: Replace OP constants and send functions**

In `contracts/InstructionSender.sol`, replace the greeting constants and the two send functions (keep constructor, `setExtensionId`, `_getExtensionId`, registries). New body:
```solidity
    // forge-lint: disable-next-line(unsafe-typecast)
    bytes32 public constant OP_TYPE_HL = bytes32("HL");
    // forge-lint: disable-next-line(unsafe-typecast)
    bytes32 public constant OP_COMMAND_DEPOSIT = bytes32("DEPOSIT");
    // forge-lint: disable-next-line(unsafe-typecast)
    bytes32 public constant OP_COMMAND_PLACE_ORDER = bytes32("PLACE_ORDER");
    // forge-lint: disable-next-line(unsafe-typecast)
    bytes32 public constant OP_COMMAND_CANCEL = bytes32("CANCEL");
    // forge-lint: disable-next-line(unsafe-typecast)
    bytes32 public constant OP_COMMAND_WITHDRAW = bytes32("WITHDRAW");

    /// @notice Emits an HL instruction, embedding msg.sender so the TEE can
    /// derive the caller's dedicated Hyperliquid account.
    function _send(bytes32 opCommand, bytes memory _payload) internal {
        address[] memory teeIds = TEE_MACHINE_REGISTRY.getRandomTeeIds(_getExtensionId(), 1);
        address[] memory cosigners = new address[](0);

        bytes memory message = abi.encode(msg.sender, _payload);

        ITeeExtensionRegistry.TeeInstructionParams memory params = ITeeExtensionRegistry.TeeInstructionParams({
            opType: OP_TYPE_HL,
            opCommand: opCommand,
            message: message,
            cosigners: cosigners,
            cosignersThreshold: 0,
            claimBackAddress: msg.sender
        });

        TEE_EXTENSION_REGISTRY.sendInstructions{value: msg.value}(teeIds, params);
    }

    /// @notice Credit the caller's TEE-managed account (mock deposit in v1).
    /// @param _payload JSON e.g. {"usdAmount":100}
    function deposit(bytes calldata _payload) external payable {
        _send(OP_COMMAND_DEPOSIT, _payload);
    }

    /// @notice Place a spot or perp order.
    /// @param _payload JSON e.g. {"market":"perp","coin":"BTC","isBuy":true,"orderType":"market","size":0.01,"leverage":5}
    function placeOrder(bytes calldata _payload) external payable {
        _send(OP_COMMAND_PLACE_ORDER, _payload);
    }

    /// @notice Cancel an open order.
    /// @param _payload JSON e.g. {"coin":"BTC","orderId":123}
    function cancel(bytes calldata _payload) external payable {
        _send(OP_COMMAND_CANCEL, _payload);
    }

    /// @notice Withdraw USD from the caller's TEE-managed account (internal stub in v1).
    /// @param _payload JSON e.g. {"usdAmount":50}
    function withdraw(bytes calldata _payload) external payable {
        _send(OP_COMMAND_WITHDRAW, _payload);
    }
```
Also delete the `SayGoodbyeMessage` struct.

- [ ] **Step 2: Compile and regenerate bindings**

Run:
```bash
cd /Users/snojj25/Desktop/505/Flare/tee/extension-examples/hype-accounts && forge build && ./scripts/generate-bindings.sh
```
Expected: contract compiles; `tools/pkg/contracts/hyperliquid/autogen.go` regenerated with `Deposit`, `PlaceOrder`, `Cancel`, `Withdraw` methods.

- [ ] **Step 3: Commit**

```bash
cd /Users/snojj25/Desktop/505/Flare/tee/extension-examples/hype-accounts && git add contracts/ tools/pkg/contracts/ && git commit -m "feat: HL InstructionSender with deposit/order/cancel/withdraw and msg.sender envelope"
```

---

### Task 13: Tools — instruction send helpers

**Files:**
- Modify: `tools/pkg/utils/instructions.go`
- Test: build-only (these wrap on-chain calls; covered by Task 14 e2e).

**Interfaces:**
- Consumes: `hyperliquid.HyperliquidInstructionSender` bindings.
- Produces: `SendDeposit(s, addr, payload []byte)`, `SendPlaceOrder(s, addr, payload []byte)`, `SendCancel(s, addr, payload []byte)`, `SendWithdraw(s, addr, payload []byte)` — each returns `(instructionID, txHash common.Hash, err error)`.

- [ ] **Step 1: Replace SendSayHello/SendSayGoodbye with HL helpers**

In `tools/pkg/utils/instructions.go`, keep `DeployInstructionSender` and `SetExtensionId` (already renamed to `hyperliquid.` in Task 1). Replace the two `Send*` greeting helpers with a generic helper plus four wrappers:
```go
func sendHL(
	s *support.Support,
	instructionSenderAddress common.Address,
	call func(opts *bind.TransactOpts, payload []byte) (*types.Transaction, error),
	payload []byte,
) (common.Hash, common.Hash, error) {
	opts, err := bind.NewKeyedTransactorWithChainID(s.Prv, s.ChainID)
	if err != nil {
		return common.Hash{}, common.Hash{}, errors.Errorf("failed to create transactor: %s", err)
	}
	opts.Value = big.NewInt(1000000) // instruction fee in wei — must match registry's required fee

	tx, err := call(opts, payload)
	if err != nil {
		return common.Hash{}, common.Hash{}, errors.Errorf("failed to send instruction: %s", err)
	}
	receipt, err := bind.WaitMined(context.Background(), s.ChainClient, tx)
	if err != nil {
		return common.Hash{}, common.Hash{}, errors.Errorf("failed waiting for transaction: %s", err)
	}
	if receipt.Status != 1 {
		return common.Hash{}, common.Hash{}, errors.Errorf("transaction failed with status: %d", receipt.Status)
	}
	if len(receipt.Logs) == 0 {
		return common.Hash{}, common.Hash{}, errors.New("no logs found in receipt")
	}
	sent, err := s.TeeVerification.ParseTeeInstructionsSent(*receipt.Logs[0])
	if err != nil {
		return common.Hash{}, common.Hash{}, errors.Errorf("failed to parse TeeInstructionsSent event: %s", err)
	}
	return sent.InstructionId, receipt.TxHash, nil
}

func bindSender(s *support.Support, addr common.Address) (*hyperliquid.HyperliquidInstructionSender, error) {
	return hyperliquid.NewHyperliquidInstructionSender(addr, s.ChainClient)
}

func SendDeposit(s *support.Support, addr common.Address, payload []byte) (common.Hash, common.Hash, error) {
	c, err := bindSender(s, addr)
	if err != nil {
		return common.Hash{}, common.Hash{}, err
	}
	return sendHL(s, addr, c.Deposit, payload)
}

func SendPlaceOrder(s *support.Support, addr common.Address, payload []byte) (common.Hash, common.Hash, error) {
	c, err := bindSender(s, addr)
	if err != nil {
		return common.Hash{}, common.Hash{}, err
	}
	return sendHL(s, addr, c.PlaceOrder, payload)
}

func SendCancel(s *support.Support, addr common.Address, payload []byte) (common.Hash, common.Hash, error) {
	c, err := bindSender(s, addr)
	if err != nil {
		return common.Hash{}, common.Hash{}, err
	}
	return sendHL(s, addr, c.Cancel, payload)
}

func SendWithdraw(s *support.Support, addr common.Address, payload []byte) (common.Hash, common.Hash, error) {
	c, err := bindSender(s, addr)
	if err != nil {
		return common.Hash{}, common.Hash{}, err
	}
	return sendHL(s, addr, c.Withdraw, payload)
}
```
Ensure imports include `"github.com/ethereum/go-ethereum/core/types"` (already present) for `*types.Transaction`.
> **Implementer note:** confirm the generated method signature is `func (c *HyperliquidInstructionSender) Deposit(opts *bind.TransactOpts, _payload []byte) (*types.Transaction, error)`. If abigen names the param differently it does not matter; the function value `c.Deposit` must match `func(*bind.TransactOpts, []byte) (*types.Transaction, error)`.

- [ ] **Step 2: Build tools**

Run: `cd /Users/snojj25/Desktop/505/Flare/tee/extension-examples/hype-accounts/tools && go build ./...`
Expected: success.

- [ ] **Step 3: Commit**

```bash
cd /Users/snojj25/Desktop/505/Flare/tee/extension-examples/hype-accounts && git add tools/pkg/utils/instructions.go && git commit -m "feat: tools send helpers for HL instructions"
```

---

### Task 14: End-to-end test harness (`run-test`)

**Files:**
- Modify: `tools/cmd/run-test/main.go`
- Reference: `docs/testing.md`

**Interfaces:**
- Consumes: `utils.SendDeposit/SendPlaceOrder/SendCancel/SendWithdraw`, the proxy result-polling helper already used by the scaffold's run-test.

- [ ] **Step 1: Read the existing run-test to learn the poll pattern**

Run: `cd /Users/snojj25/Desktop/505/Flare/tee/extension-examples/hype-accounts && sed -n '1,200p' tools/cmd/run-test/main.go` and identify the helper that sends an instruction then polls `EXT_PROXY_URL` for the result by instruction ID.

- [ ] **Step 2: Replace the greeting test sequence with the HL sequence**

Rewrite the test body to:
1. Build a deposit payload `{"usdAmount":100}` → `utils.SendDeposit` → poll result → assert status success and capture `hlAddress`.
2. Build a perp market order `{"market":"perp","coin":"ETH","isBuy":true,"orderType":"market","size":0.01,"leverage":3}` → `utils.SendPlaceOrder` → poll → assert accepted.
3. Build a spot limit order `{"market":"spot","coin":"PURR/USDC","isBuy":true,"orderType":"limit","size":20,"limitPx":0.5}` → `utils.SendPlaceOrder` → poll → assert accepted.
4. Build a withdraw `{"usdAmount":20}` → `utils.SendWithdraw` → poll → assert success.

Use the existing poll helper; marshal payloads with `json.Marshal`. Example for the first step:
```go
depPayload, _ := json.Marshal(map[string]any{"usdAmount": 100})
instrID, _, err := utils.SendDeposit(s, instructionSender, depPayload)
if err != nil {
	log.Fatalf("send deposit: %v", err)
}
res := pollResult(proxyURL, instrID) // existing helper
if res.Status != 1 {
	log.Fatalf("deposit failed: %s", res.Log)
}
log.Printf("deposit ok: %s", string(res.Data))
```
> **Implementer note:** reuse the exact poll function name/signature found in Step 1; do not invent a new one. Keep the env-var wiring (`INSTRUCTION_SENDER`, `EXT_PROXY_URL`) identical to the scaffold.

- [ ] **Step 3: Build**

Run: `cd /Users/snojj25/Desktop/505/Flare/tee/extension-examples/hype-accounts/tools && go build ./...`
Expected: success. (Full run requires live infra + a funded treasury — see Task 16 run instructions.)

- [ ] **Step 4: Commit**

```bash
cd /Users/snojj25/Desktop/505/Flare/tee/extension-examples/hype-accounts && git add tools/cmd/run-test/ && git commit -m "test: end-to-end HL deposit/order/cancel/withdraw harness"
```

---

### Task 15: HL testnet integration test (build-tagged)

**Files:**
- Create: `testing/hl_testnet_test.go`

**Interfaces:**
- Consumes: `hl.NewRealFactory`, `keys.DeriveUserKey`, env `HL_TREASURY_KEY`, `HL_MASTER_SEED`.

- [ ] **Step 1: Write the integration test**

Create `testing/hl_testnet_test.go`:
```go
//go:build hltestnet

package testing

import (
	"context"
	"os"
	"testing"

	"extension-scaffold/internal/hl"
	"extension-scaffold/internal/keys"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// Run with: HL_TREASURY_KEY=... HL_MASTER_SEED=... go test -tags hltestnet ./testing/ -run TestHLTestnetRoundTrip -v
func TestHLTestnetRoundTrip(t *testing.T) {
	tkHex := os.Getenv("HL_TREASURY_KEY")
	seedHex := os.Getenv("HL_MASTER_SEED")
	if tkHex == "" || seedHex == "" {
		t.Skip("set HL_TREASURY_KEY and HL_MASTER_SEED")
	}
	seed, err := keys.MustHexSeed(seedHex)
	if err != nil {
		t.Fatal(err)
	}
	tk, err := crypto.HexToECDSA(tkHex)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	factory := hl.NewRealFactory("https://api.hyperliquid-testnet.xyz")
	treasuryAddr := crypto.PubkeyToAddress(tk.PublicKey).Hex()
	treasury := factory.Account(tk, treasuryAddr)

	user := common.HexToAddress("0x000000000000000000000000000000000000dEaD")
	uk, _ := keys.DeriveUserKey(seed, user)
	uAddr := crypto.PubkeyToAddress(uk.PublicKey).Hex()
	uAcct := factory.Account(uk, uAddr)

	if err := treasury.SendUSD(ctx, uAddr, 15); err != nil {
		t.Fatalf("fund user from treasury: %v", err)
	}
	// Perp market buy, min ~$10 notional.
	if _, err := uAcct.PlaceOrder(ctx, hl.OrderReq{
		Coin: "ETH", Market: "perp", IsBuy: true, OrderType: "market", Size: 0.005, Leverage: 2,
	}); err != nil {
		t.Fatalf("place order: %v", err)
	}
	pos, err := uAcct.Positions(ctx)
	if err != nil {
		t.Fatalf("positions: %v", err)
	}
	t.Logf("positions: %+v", pos)
}
```

- [ ] **Step 2: Verify it compiles (skipped without creds)**

Run: `cd /Users/snojj25/Desktop/505/Flare/tee/extension-examples/hype-accounts && go test -tags hltestnet ./testing/ -run TestHLTestnetRoundTrip -v`
Expected: SKIP (no creds) — confirms it compiles. With real creds + a faucet-funded treasury, it executes a real round-trip.

- [ ] **Step 3: Commit**

```bash
cd /Users/snojj25/Desktop/505/Flare/tee/extension-examples/hype-accounts && git add testing/ && git commit -m "test: HL testnet integration round-trip (build-tagged)"
```

---

### Task 16: Env, docs, and operator runbook

**Files:**
- Modify: `.env.example`
- Modify: `README.md`

- [ ] **Step 1: Add HL env vars to .env.example**

Append to `.env.example` (do NOT touch `.env`):
```bash
# --- Hyperliquid Controlled Accounts ---
# Testnet API (signing domain is selected by this URL).
HYPERLIQUID_URL=https://api.hyperliquid-testnet.xyz
# 32+ byte hex seed used to derive per-user HL keys (KEEP SECRET).
HL_MASTER_SEED=
# Private key (hex) of the faucet-funded treasury HL account (KEEP SECRET).
HL_TREASURY_KEY=
# Caps (USD) for the v1 mock deposit path.
HL_MAX_DEPOSIT_USD=1000
HL_MAX_TOTAL_DEPOSIT_USD=5000
```

- [ ] **Step 2: Document the runbook in README**

Add a "Hyperliquid Controlled Accounts" section to `README.md` covering: (a) seed the treasury by claiming the testnet faucet (1,000 mock USDC at `app.hyperliquid-testnet.xyz/drip`, requires $5 mainnet activation) for the `HL_TREASURY_KEY` address; (b) set the env vars; (c) the four instructions and their JSON payloads (copy the table from the design spec §5); (d) the read path (per-user `hlAddress` from `/state`, plus reading HL's public info API directly); (e) the v1 deferrals (FSA, real FXRP, multi-machine — link the design spec).

- [ ] **Step 3: Final full build + unit tests**

Run:
```bash
cd /Users/snojj25/Desktop/505/Flare/tee/extension-examples/hype-accounts && go build ./... && go test ./internal/... ./pkg/... -v && cd tools && go build ./...
```
Expected: all green.

- [ ] **Step 4: Commit**

```bash
cd /Users/snojj25/Desktop/505/Flare/tee/extension-examples/hype-accounts && git add .env.example README.md && git commit -m "docs: HL extension env vars and operator runbook"
```

---

## Self-Review

**Spec coverage:**
- §3 Identity (msg.sender envelope) → Tasks 6 (`DecodeEnvelope`), 12 (contract `abi.encode(msg.sender,...)`). ✓
- §4 Per-user accounts: deterministic derivation → Task 3; treasury funding via usdSend → Task 8; spot/perp class transfer → Task 9; caps → Tasks 2, 8. ✓
- §5 Instruction set (DEPOSIT/PLACE_ORDER/CANCEL/WITHDRAW) → Tasks 8–12. ✓
- §6 Extension internals (struct, cache, ledger, async) → Tasks 4, 7. ✓
- §7 Read path ("both"): TEE authoritative state + exposed HL address → Task 7 (`stateHandler` exposes per-user `hlAddress` + ledger); frontend-direct read documented → Task 16. (Optional signed `QUERY` action deliberately omitted from v1; `Positions()` exists on `Account` for a later QUERY op.) ✓ (partial-by-design)
- §8 Testing (unit + HL testnet integration + e2e) → Tasks 3–11 (unit), 15 (integration), 14 (e2e). ✓
- §9 Deferred items → not implemented, documented in Task 16. ✓
- §10 Risks: single TEE machine (no multi-machine routing added); unbacked-deposit caps (Tasks 2/8); approximate spot funding (Task 9 note). ✓

**Placeholder scan:** No "TBD"/"add error handling"-style placeholders. The two `Implementer note` blocks (Task 5 field names, Task 6 types-server decoder) flag exact source-verification points with concrete fallbacks, not vague gaps.

**Type consistency:** `Account` interface methods (`PlaceOrder`, `Cancel`, `SendUSD`, `ClassTransfer`, `SpotUSDC`, `SetLeverage`, `Positions`, `Address`) are used identically in `FakeAccount` (Task 5) and all handlers (Tasks 8–11). `types.DecodeEnvelope`, `ledger.OrderRecord`, `hl.OrderReq` field names match across producer and consumer tasks. Contract OP strings (`HL`, `DEPOSIT`, `PLACE_ORDER`, `CANCEL`, `WITHDRAW`) match `config` constants exactly.

**Known verification points for the implementer (network-coupled, cannot be unit-tested):**
1. `go-hyperliquid` struct field names in `internal/hl/account.go` (Task 5 note).
2. types-server envelope decoding registration (Task 6 note).
3. Generated binding method signatures (Task 13 note).
4. The `run-test` poll helper name (Task 14 note).
