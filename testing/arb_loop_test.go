//go:build hltestnet

package testing

import (
	"context"
	"math/big"
	"os"
	"strings"
	"testing"
	"time"

	"extension-scaffold/internal/arb"
	"extension-scaffold/internal/hl"
	"extension-scaffold/internal/keys"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// Self-contained Arbitrum Sepolia <-> Hyperliquid testnet defaults. The bridge
// only credits deposits of its own mock USDC (ARB_USDC_ADDRESS), so a public
// USDC faucet will not work; the reserve bootstraps its USDC by withdrawing the
// treasury's HL balance out to Arbitrum.
const (
	defaultArbRPC  = "https://sepolia-rollup.arbitrum.io/rpc"
	defaultArbUSDC = "0x1baAbB04529D43a73232B713C0FE471f7c7334d5"
	defaultBridge2 = "0x08cfc1B6b2dCF36A1480b99353A354AA8AC56f89"
	hlTestnetURL   = "https://api.hyperliquid-testnet.xyz"

	depositUSD    = 40.0
	withdrawUSD   = 5.0
	userGasWei    = 1_000_000_000_000_000 // 0.001 ETH dust for the user's bridge-deposit gas
	minUserGasWei = 200_000_000_000_000   // 0.0002 ETH — top up below this
)

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// hlTotalUSD returns the account's total USDC across the perp (Core) and spot
// balances — robust to whether a Bridge2 deposit lands in the perp balance
// (standard mode) or is reported in spot (unified mode).
func hlTotalUSD(ctx context.Context, a hl.Account) (float64, error) {
	pos, err := a.Positions(ctx)
	if err != nil {
		return 0, err
	}
	return pos.PerpAccountValueUSD + pos.SpotUSDC, nil
}

// TestArbHLLoop exercises the whole Arbitrum Sepolia <-> HL testnet funding loop
// end to end, each leg a subtest so a failure localizes to one hop:
//
//  1. bootstrap_reserve_usdc — withdraw HL balance out to the Arbitrum reserve
//     (also proves the withdraw leg and mints the bridge's mock USDC to us)
//  2. fund_user_address      — reserve sends the user gas + USDC on Arbitrum
//  3. deposit_to_hl          — user transfers USDC to Bridge2; HL credits the user
//  4. trade_perp             — user opens a small perp from the unified balance
//  5. withdraw_to_arbitrum   — user withdraw3s back to their Arbitrum address
//
// Prereqs: the reserve key must be funded on HL testnet (faucet /drip) AND hold
// a little Arbitrum Sepolia ETH for gas.
//
//	HL_TREASURY_KEY=... [ARB_RESERVE_KEY=...] [ARBITRUM_RPC_URL=...] \
//	  go test -tags hltestnet ./testing/ -run TestArbHLLoop -v -timeout 30m
func TestArbHLLoop(t *testing.T) {
	reserveHex := envOr("ARB_RESERVE_KEY", os.Getenv("HL_TREASURY_KEY"))
	if reserveHex == "" {
		t.Skip("set HL_TREASURY_KEY (or ARB_RESERVE_KEY): reserve funded on HL testnet + with Arbitrum Sepolia ETH")
	}
	reserveKey, err := crypto.HexToECDSA(strings.TrimPrefix(reserveHex, "0x"))
	if err != nil {
		t.Fatalf("reserve key: %v", err)
	}
	reserveAddr := crypto.PubkeyToAddress(reserveKey.PublicKey)

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()

	client, err := arb.New(ctx, envOr("ARBITRUM_RPC_URL", defaultArbRPC))
	if err != nil {
		t.Fatalf("arbitrum client: %v", err)
	}
	defer client.Close()

	usdc := common.HexToAddress(envOr("ARB_USDC_ADDRESS", defaultArbUSDC))
	bridge := common.HexToAddress(envOr("HL_BRIDGE2_ADDRESS", defaultBridge2))

	factory := hl.NewRealFactory(hlTestnetURL)
	reserveHL := factory.Account(reserveKey, reserveAddr.Hex())

	// A stable throwaway "user" derived from the reserve key, so re-runs reuse
	// the same testnet account (balance accumulates rather than scattering).
	userKey, err := keys.DeriveUserKey(crypto.FromECDSA(reserveKey),
		common.HexToAddress("0x000000000000000000000000000000000000dEaD"))
	if err != nil {
		t.Fatalf("derive user key: %v", err)
	}
	userAddr := crypto.PubkeyToAddress(userKey.PublicKey)
	userHL := factory.Account(userKey, userAddr.Hex())

	t.Logf("chain=%s reserve=%s user=%s", client.ChainID(), reserveAddr.Hex(), userAddr.Hex())

	// Precondition: the reserve pays Arbitrum gas for the funding transfers.
	ethBal, err := client.ETHBalance(ctx, reserveAddr)
	if err != nil {
		t.Fatalf("reserve ETH balance: %v", err)
	}
	t.Logf("reserve Arbitrum ETH: %s wei", ethBal)
	if ethBal.Sign() == 0 {
		t.Fatalf("reserve %s has no Arbitrum Sepolia ETH — fund it from a faucet "+
			"(e.g. https://faucet.quicknode.com/arbitrum/sepolia) and re-run", reserveAddr.Hex())
	}

	// 1) Ensure the reserve holds enough of the bridge's mock USDC, topping up
	//    from the HL side if needed. This doubles as the withdraw-leg proof.
	if !t.Run("bootstrap_reserve_usdc", func(t *testing.T) {
		bal, err := client.USDCBalance(ctx, usdc, reserveAddr)
		if err != nil {
			t.Fatalf("reserve USDC balance: %v", err)
		}
		have := arb.UnitsToUSD(bal)
		need := depositUSD + 10 // deposit + gas/fee headroom
		t.Logf("reserve Arbitrum USDC: %.2f (need >= %.2f)", have, need)
		if have >= need {
			t.Logf("reserve already funded; skipping HL bootstrap withdraw")
			return
		}
		move := need - have + 3 // bring this over (+3 covers the flat withdraw fee)
		// The reserve holds its USDC in spot (standard-mode account), but
		// withdraw3 pulls from the perp/withdrawable balance — so move
		// spot->perp first, then withdraw a little less than we moved.
		t.Logf("bootstrapping: %.2f USDC spot->perp, then withdraw3 -> Arbitrum", move)
		if err := reserveHL.ClassTransfer(ctx, move, true); err != nil {
			t.Fatalf("spot->perp before withdraw: %v", err)
		}
		if err := reserveHL.Withdraw(ctx, reserveAddr.Hex(), move-2); err != nil {
			t.Fatalf("HL bootstrap withdraw: %v — ensure the reserve's HL testnet "+
				"account is funded via the faucet (/drip)", err)
		}
		want := arb.USDToUnits(need - 2) // ~need lands after the flat fee
		got, err := client.WaitForUSDCBalance(ctx, usdc, reserveAddr, want, 10*time.Second, 6*time.Minute)
		if err != nil {
			t.Fatalf("waiting for bootstrap withdraw on Arbitrum: %v", err)
		}
		t.Logf("reserve Arbitrum USDC after bootstrap: %.2f", arb.UnitsToUSD(got))
	}) {
		t.Fatal("bootstrap failed; aborting loop")
	}

	// 2) Position gas + USDC at the user's own Arbitrum address (Bridge2 credits
	//    the sending address, so the deposit must originate from the user).
	if !t.Run("fund_user_address", func(t *testing.T) {
		uEth, err := client.ETHBalance(ctx, userAddr)
		if err != nil {
			t.Fatalf("user ETH balance: %v", err)
		}
		if uEth.Cmp(big.NewInt(minUserGasWei)) < 0 {
			if _, err := client.SendETH(ctx, reserveKey, userAddr, big.NewInt(userGasWei)); err != nil {
				t.Fatalf("fund user gas: %v", err)
			}
			t.Logf("sent 0.001 ETH gas to user")
		}
		if _, err := client.TransferUSDC(ctx, reserveKey, usdc, userAddr, arb.USDToUnits(depositUSD)); err != nil {
			t.Fatalf("fund user USDC: %v", err)
		}
		got, err := client.WaitForUSDCBalance(ctx, usdc, userAddr, arb.USDToUnits(depositUSD), 5*time.Second, 2*time.Minute)
		if err != nil {
			t.Fatalf("waiting for user USDC: %v", err)
		}
		t.Logf("user Arbitrum USDC: %.2f", arb.UnitsToUSD(got))
	}) {
		t.Fatal("user funding failed; aborting loop")
	}

	// 3) Deposit to Bridge2 (a plain USDC transfer) and wait for the HL credit.
	if !t.Run("deposit_to_hl", func(t *testing.T) {
		before, err := hlTotalUSD(ctx, userHL)
		if err != nil {
			t.Fatalf("read user HL balance: %v", err)
		}
		t.Logf("user HL total USDC before deposit: %.2f", before)
		if _, err := client.DepositToBridge(ctx, userKey, usdc, bridge, arb.USDToUnits(depositUSD)); err != nil {
			t.Fatalf("deposit to bridge: %v", err)
		}
		t.Logf("submitted Bridge2 deposit of %.2f USDC; polling HL for credit", depositUSD)
		deadline := time.Now().Add(4 * time.Minute)
		for {
			cur, err := hlTotalUSD(ctx, userHL)
			if err == nil && cur >= before+depositUSD-1 {
				t.Logf("HL credited: user total USDC now %.2f", cur)
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for HL to credit deposit (last=%.2f)", cur)
			}
			select {
			case <-ctx.Done():
				t.Fatalf("context: %v", ctx.Err())
			case <-time.After(10 * time.Second):
			}
		}
	}) {
		t.Fatal("deposit failed; aborting loop")
	}

	// 4) Trade a small perp. On a default (unified) account the perp draws from
	//    the single balance — no spot->perp sweep needed.
	t.Run("trade_perp", func(t *testing.T) {
		res, err := userHL.PlaceOrder(ctx, hl.OrderReq{
			Coin: "ETH", Market: "perp", IsBuy: true, OrderType: "market", Size: 0.02, Leverage: 2,
		})
		if err != nil {
			t.Fatalf("place order: %v", err)
		}
		t.Logf("order result: %+v", res)
		pos, err := userHL.Positions(ctx)
		if err != nil {
			t.Fatalf("positions: %v", err)
		}
		t.Logf("positions: %+v", pos)
	})

	// 5) Withdraw part of the free balance back to Arbitrum (closes the loop).
	t.Run("withdraw_to_arbitrum", func(t *testing.T) {
		before, err := client.USDCBalance(ctx, usdc, userAddr)
		if err != nil {
			t.Fatalf("user Arbitrum USDC: %v", err)
		}
		t.Logf("user Arbitrum USDC before withdraw: %.2f", arb.UnitsToUSD(before))
		if err := userHL.Withdraw(ctx, userAddr.Hex(), withdrawUSD); err != nil {
			t.Fatalf("HL withdraw3: %v", err)
		}
		t.Logf("submitted withdraw3 of %.2f USDC -> %s", withdrawUSD, userAddr.Hex())
		want := new(big.Int).Add(before, arb.USDToUnits(withdrawUSD-2)) // minus the flat fee
		got, err := client.WaitForUSDCBalance(ctx, usdc, userAddr, want, 15*time.Second, 6*time.Minute)
		if err != nil {
			t.Fatalf("waiting for withdrawal on Arbitrum: %v", err)
		}
		t.Logf("user Arbitrum USDC after withdraw: %.2f", arb.UnitsToUSD(got))
	})
}
