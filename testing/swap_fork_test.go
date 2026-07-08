//go:build hltestnet

package testing

import (
	"context"
	"fmt"
	"math/big"
	"os"
	"strings"
	"testing"
	"time"

	"extension-scaffold/internal/swap"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rpc"
)

// Swap-client proof on an anvil fork of Arbitrum One (Task 2.3 / C12). Testnet
// DEX liquidity is unreliable, so the pragmatic proof (as the plan notes) is a
// mainnet fork: real Uniswap V3 bytecode, real USDT/USDC liquidity, zero real
// funds. The fork cheatcodes (anvil_setBalance, anvil_impersonateAccount) fund
// a throwaway key with USDT taken from the pool itself, then the client quotes
// and swaps through BOTH router shapes (classic SwapRouter with deadline — the
// shape SparkDEX-style forks use — and SwapRouter02).
//
// Start the fork, then run:
//
//	anvil --fork-url https://arb1.arbitrum.io/rpc --port 8545
//	go test -tags hltestnet ./testing/ -run TestSwapForkArbitrumOne -v
const (
	arbOneUSDT       = "0xFd086bC7CD5C481DCC9C85ebE478A1C0b69FCbb9"
	arbOneUSDC       = "0xaf88d065e77c8cC2239327C5EDb3A432268e5831" // native USDC (what Bridge2 takes)
	arbOneV3Factory  = "0x1F98431c8aD98523631AE4a59f267346ea31F984"
	arbOneV3Router   = "0xE592427A0AEce92De3Edee1F18E0157C05861564" // classic SwapRouter
	arbOneV3Router02 = "0x68b3465833fb72A70ecDF485E0e4C7bD8665Fc45" // SwapRouter02
	arbOneQuoterV2   = "0x61fFE014bA17989E743c5F6cB21bF9697530B21e"
)

func TestSwapForkArbitrumOne(t *testing.T) {
	forkRPC := os.Getenv("SWAP_FORK_RPC")
	if forkRPC == "" {
		forkRPC = "http://127.0.0.1:8545"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	rpcc, err := rpc.DialContext(ctx, forkRPC)
	if err != nil {
		t.Skipf("no fork RPC at %s (%v) — start: anvil --fork-url https://arb1.arbitrum.io/rpc", forkRPC, err)
	}
	defer rpcc.Close()
	// Confirm it is an Arbitrum One fork (and reachable) before spending time.
	var chainIDHex string
	if err := rpcc.CallContext(ctx, &chainIDHex, "eth_chainId"); err != nil {
		t.Skipf("fork RPC not responding at %s (%v) — start: anvil --fork-url https://arb1.arbitrum.io/rpc", forkRPC, err)
	}
	if chainIDHex != "0xa4b1" { // 42161
		t.Fatalf("fork chain id %s, want Arbitrum One (0xa4b1) — wrong --fork-url?", chainIDHex)
	}

	c, err := swap.New(ctx, forkRPC)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	key, err := crypto.HexToECDSA(strings.Repeat("ab", 32))
	if err != nil {
		t.Fatal(err)
	}
	addr := crypto.PubkeyToAddress(key.PublicKey)
	usdt := common.HexToAddress(arbOneUSDT)
	usdc := common.HexToAddress(arbOneUSDC)

	// Cheatcode funding: gas for the test key.
	if err := rpcc.CallContext(ctx, nil, "anvil_setBalance", addr, "0x8AC7230489E80000"); err != nil { // 10 ETH
		t.Fatalf("anvil_setBalance (is this anvil?): %v", err)
	}

	// Find the USDT/USDC pool and take 200 USDT from it (a fork; pool solvency
	// for a dust-sized swap is unaffected — V3 swap math runs on liquidity
	// state, not raw balances).
	factoryABI, err := abi.JSON(strings.NewReader(`[{"name":"getPool","type":"function","stateMutability":"view","inputs":[{"name":"a","type":"address"},{"name":"b","type":"address"},{"name":"fee","type":"uint24"}],"outputs":[{"name":"","type":"address"}]}]`))
	if err != nil {
		t.Fatal(err)
	}
	var pool common.Address
	var feeTier uint32
	for _, ft := range []uint32{100, 500} {
		data, err := factoryABI.Pack("getPool", usdt, usdc, big.NewInt(int64(ft)))
		if err != nil {
			t.Fatal(err)
		}
		var out string
		if err := rpcc.CallContext(ctx, &out, "eth_call", map[string]string{"to": arbOneV3Factory, "data": "0x" + common.Bytes2Hex(data)}, "latest"); err != nil {
			t.Fatal(err)
		}
		p := common.HexToAddress(out)
		if p != (common.Address{}) {
			pool, feeTier = p, ft
			break
		}
	}
	if pool == (common.Address{}) {
		t.Fatal("no USDT/USDC pool at fee 100 or 500")
	}
	t.Logf("USDT/USDC pool %s (fee tier %d)", pool.Hex(), feeTier)

	erc20ABI, err := abi.JSON(strings.NewReader(`[{"constant":false,"inputs":[{"name":"to","type":"address"},{"name":"amount","type":"uint256"}],"name":"transfer","outputs":[{"name":"","type":"bool"}],"type":"function"}]`))
	if err != nil {
		t.Fatal(err)
	}
	xfer, err := erc20ABI.Pack("transfer", addr, big.NewInt(200_000_000)) // 200 USDT
	if err != nil {
		t.Fatal(err)
	}
	if err := rpcc.CallContext(ctx, nil, "anvil_impersonateAccount", pool); err != nil {
		t.Fatalf("impersonate: %v", err)
	}
	if err := rpcc.CallContext(ctx, nil, "anvil_setBalance", pool, "0xDE0B6B3A7640000"); err != nil { // 1 ETH gas
		t.Fatal(err)
	}
	var txh string
	if err := rpcc.CallContext(ctx, &txh, "eth_sendTransaction", map[string]string{
		"from": pool.Hex(), "to": usdt.Hex(), "data": "0x" + common.Bytes2Hex(xfer),
	}); err != nil {
		t.Fatalf("impersonated USDT transfer: %v", err)
	}
	_ = rpcc.CallContext(ctx, nil, "anvil_stopImpersonatingAccount", pool)

	usdtBal, err := c.BalanceOf(ctx, usdt, addr)
	if err != nil {
		t.Fatal(err)
	}
	if usdtBal.Cmp(big.NewInt(100_000_000)) < 0 {
		t.Fatalf("USDT funding failed: balance %s", usdtBal)
	}
	t.Logf("funded: %s USDT units at %s", usdtBal, addr.Hex())

	amountIn := big.NewInt(50_000_000) // 50 USDT per swap
	quoter := common.HexToAddress(arbOneQuoterV2)

	// Exercise both router shapes against the real bytecode.
	for _, rt := range []struct {
		name   string
		router common.Address
		kind   swap.RouterKind
	}{
		{"classic SwapRouter", common.HexToAddress(arbOneV3Router), swap.RouterClassic},
		{"SwapRouter02", common.HexToAddress(arbOneV3Router02), swap.Router02},
	} {
		t.Run(rt.name, func(t *testing.T) {
			quote, err := c.QuoteExactIn(ctx, quoter, usdt, usdc, feeTier, amountIn)
			if err != nil {
				t.Fatalf("quote: %v", err)
			}
			// Sanity: 50 USDT should quote to ~50 USDC (both 6 decimals).
			if quote.Cmp(big.NewInt(49_000_000)) < 0 || quote.Cmp(big.NewInt(51_000_000)) > 0 {
				t.Fatalf("implausible USDT->USDC quote: %s for %s in", quote, amountIn)
			}
			minOut := new(big.Int).Div(new(big.Int).Mul(quote, big.NewInt(99)), big.NewInt(100)) // 1% tolerance

			before, err := c.BalanceOf(ctx, usdc, addr)
			if err != nil {
				t.Fatal(err)
			}
			txh, err := c.SwapExactIn(ctx, key, rt.router, rt.kind, usdt, usdc, feeTier, addr, amountIn, minOut)
			if err != nil {
				t.Fatalf("swap: %v", err)
			}
			after, err := c.BalanceOf(ctx, usdc, addr)
			if err != nil {
				t.Fatal(err)
			}
			got := new(big.Int).Sub(after, before)
			if got.Cmp(minOut) < 0 {
				t.Fatalf("received %s < minOut %s", got, minOut)
			}
			t.Logf("swap ok via %s: tx=%s in=%s USDT out=%s USDC (quote %s, minOut %s)",
				rt.name, txh.Hex(), amountIn, got, quote, minOut)
		})
	}
	_ = fmt.Sprintf // keep fmt if assertions above change
}
