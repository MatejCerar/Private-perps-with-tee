//go:build hltestnet

package extension

import (
	"context"
	"fmt"
	"math"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"extension-scaffold/internal/arb"
	"extension-scaffold/internal/hl"
	"extension-scaffold/pkg/types"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// TestBridgeMultiUserLive is the Task 1.6 final live gate: several DISTINCT
// derived users deposit in parallel through the real handler against Arbitrum
// Sepolia + HL testnet, then drain back to the reserve in parallel. It proves,
// live, what the 20-way unit test proves with fakes:
//
//   - the reserve key's nonce serialization (arb.Client.sendMu) survives real
//     concurrent deposits (gas + USDC funding txs from one key, interleaved),
//   - per-user isolation: each user's ledger is credited exactly its own
//     deposit; HL accounts are distinct addresses,
//   - the per-user mutex + ledger stay consistent under concurrency,
//   - parallel withdraw3s all land back at the reserve.
//
//	HL_TREASURY_KEY=... go test -tags hltestnet ./internal/extension/ -run TestBridgeMultiUserLive -v -timeout 30m
func TestBridgeMultiUserLive(t *testing.T) {
	reserveHex := os.Getenv("ARB_RESERVE_KEY")
	if reserveHex == "" {
		reserveHex = os.Getenv("HL_TREASURY_KEY")
	}
	if reserveHex == "" {
		t.Skip("set HL_TREASURY_KEY (or ARB_RESERVE_KEY): reserve funded on HL testnet + Arbitrum Sepolia ETH")
	}
	reserveKey, err := crypto.HexToECDSA(strings.TrimPrefix(reserveHex, "0x"))
	if err != nil {
		t.Fatalf("reserve key: %v", err)
	}
	reserveAddr := crypto.PubkeyToAddress(reserveKey.PublicKey)

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()

	client, err := arb.New(ctx, liveEnv("ARBITRUM_RPC_URL", "https://sepolia-rollup.arbitrum.io/rpc"))
	if err != nil {
		t.Fatalf("arb client: %v", err)
	}
	defer client.Close()

	usdc := common.HexToAddress(liveEnv("ARB_USDC_ADDRESS", "0x1baAbB04529D43a73232B713C0FE471f7c7334d5"))
	bridge := common.HexToAddress(liveEnv("HL_BRIDGE2_ADDRESS", "0x08cfc1B6b2dCF36A1480b99353A354AA8AC56f89"))

	factory := hl.NewRealFactory(hlTestnetURL)
	seed := crypto.FromECDSA(reserveKey) // deterministic users, per reserve
	e := NewWithBridge(0, 0, factory, seed, client, reserveKey, usdc, bridge)

	const (
		nUsers     = 3
		depositUSD = 6.0 // just above the 5 USDC Bridge2 minimum, keeps the run cheap
	)
	users := make([]common.Address, nUsers)
	for i := range users {
		users[i] = common.HexToAddress(fmt.Sprintf("0x000000000000000000000000000000000000D%03d", i+1))
	}

	reserveHL := factory.Account(reserveKey, reserveAddr.Hex())
	ensureReserveUSDC(t, ctx, client, reserveHL, reserveAddr, usdc, nUsers*depositUSD+10)

	// Distinct HL accounts per user (derivation sanity before spending anything).
	seen := map[string]bool{}
	for _, u := range users {
		acct, err := e.accountFor(u)
		if err != nil {
			t.Fatal(err)
		}
		if seen[acct.Address()] {
			t.Fatalf("duplicate HL address %s across users", acct.Address())
		}
		seen[acct.Address()] = true
	}

	// Parallel deposits: one goroutine per user, all through the real handler.
	t.Run("parallel_deposits", func(t *testing.T) {
		var wg sync.WaitGroup
		errs := make([]string, nUsers)
		start := make(chan struct{})
		for i, u := range users {
			wg.Add(1)
			go func(i int, u common.Address) {
				defer wg.Done()
				<-start
				a, df := actionFor(envelope(t, u, types.DepositRequest{UsdAmount: depositUSD}))
				if res := e.processDeposit(a, df); res.Status != 1 {
					errs[i] = fmt.Sprintf("user %d deposit status=%d log=%s", i, res.Status, res.Log)
				}
			}(i, u)
		}
		close(start)
		wg.Wait()
		for _, msg := range errs {
			if msg != "" {
				t.Error(msg)
			}
		}
		if t.Failed() {
			t.FailNow()
		}
		for i, u := range users {
			if got := e.led.Balance(u); got != depositUSD {
				t.Fatalf("user %d ledger=%v want %v (cross-talk or partial credit)", i, got, depositUSD)
			}
			acct, _ := e.accountFor(u)
			real, err := hlTotalUSD(ctx, acct)
			if err != nil {
				t.Fatal(err)
			}
			if real < depositUSD-depositCreditTolerance {
				t.Fatalf("user %d HL equity=%.4f < %v-%v", i, real, depositUSD, depositCreditTolerance)
			}
			t.Logf("user %d ok: hl=%s ledger=%.2f equity=%.4f", i, acct.Address(), e.led.Balance(u), real)
		}
	})

	// Parallel drains: reconcile (folds in any prior-run dust), then withdraw
	// everything but ~0.5 USDC back to the reserve, all users concurrently.
	t.Run("parallel_drain", func(t *testing.T) {
		var wg sync.WaitGroup
		errs := make([]string, nUsers)
		for i, u := range users {
			wg.Add(1)
			go func(i int, u common.Address) {
				defer wg.Done()
				if _, err := e.Reconcile(ctx, u); err != nil {
					errs[i] = fmt.Sprintf("user %d reconcile: %v", i, err)
					return
				}
				bal := e.led.Balance(u)
				if bal < 4 {
					return // too small to withdraw (min 2 + ~1 fee); leave as dust
				}
				amt := math.Floor((bal-0.5)*100) / 100
				a, df := actionFor(envelope(t, u, types.WithdrawRequest{UsdAmount: amt}))
				if res := e.processWithdraw(a, df); res.Status != 1 {
					errs[i] = fmt.Sprintf("user %d drain status=%d log=%s", i, res.Status, res.Log)
				}
			}(i, u)
		}
		wg.Wait()
		for _, msg := range errs {
			if msg != "" {
				t.Error(msg)
			}
		}
		if t.Failed() {
			t.FailNow()
		}
		for i, u := range users {
			if bal := e.led.Balance(u); bal > 1 {
				t.Fatalf("user %d not drained: ledger=%.4f", i, bal)
			}
			t.Logf("user %d drained: ledger=%.4f", i, e.led.Balance(u))
		}
	})
}
