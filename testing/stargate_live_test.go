//go:build hltestnet

package testing

import (
	"context"
	"math/big"
	"os"
	"strings"
	"testing"
	"time"

	"extension-scaffold/internal/stargate"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// Stargate V2 testnet pair used to prove the bridge client (Task 2.2 / C11).
// Source = Arbitrum Sepolia (the reserve already holds gas there), destination
// = Ethereum Sepolia. Addresses from docs.stargate.finance testnet contracts;
// each pool's token() was verified on-chain. The testnet USDC mocks expose a
// public mint, so the run is fully self-funded from existing gas.
//
// Mainnet profile (config-only swap, same client):
//
//	Flare  StargateOFTUSDC 0x77C71633C34C3784ede189d74223122422492a0f (eid 30295, token USDC.e 0xFbDa5F676cB37624f28265A144A48B0d6e87d3b6)
//	ArbOne StargatePoolUSDC 0xe8CDF27AcD73a434D661C84887215F7598e7d0d3 (eid 30110, token native USDC 0xaf88d065e77c8cC2239327C5EDb3A432268e5831)
const (
	sgSrcRPCDefault  = "https://sepolia-rollup.arbitrum.io/rpc"
	sgDstRPCDefault  = "https://ethereum-sepolia-rpc.publicnode.com"
	sgSrcPoolDefault = "0x543BdA7c6cA4384FE90B1F5929bb851F52888983" // StargatePoolUSDC, Arb Sepolia
	sgDstPoolDefault = "0x4985b8fcEA3659FD801a5b857dA1D00e985863F0" // StargatePoolUSDC, Sepolia
)

func sgEnv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// TestStargateBridgeLive proves the Stargate client end-to-end on a real
// testnet pair: quote → (mint) → send (taxi) → delivery on the destination
// chain. This is the Task 2.2 / C11 gate; on mainnet only the pool/token/eid
// config changes (Flare → Arbitrum One).
//
//	HL_TREASURY_KEY=... go test -tags hltestnet ./testing/ -run TestStargateBridgeLive -v -timeout 30m
func TestStargateBridgeLive(t *testing.T) {
	keyHex := os.Getenv("ARB_RESERVE_KEY")
	if keyHex == "" {
		keyHex = os.Getenv("HL_TREASURY_KEY")
	}
	if keyHex == "" {
		t.Skip("set HL_TREASURY_KEY (or ARB_RESERVE_KEY): needs Arbitrum Sepolia ETH for gas + LZ fee")
	}
	key, err := crypto.HexToECDSA(strings.TrimPrefix(keyHex, "0x"))
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	addr := crypto.PubkeyToAddress(key.PublicKey)

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()

	src, err := stargate.New(ctx, sgEnv("SG_SRC_RPC", sgSrcRPCDefault))
	if err != nil {
		t.Fatalf("src client: %v", err)
	}
	defer src.Close()
	dst, err := stargate.New(ctx, sgEnv("SG_DST_RPC", sgDstRPCDefault))
	if err != nil {
		t.Fatalf("dst client: %v", err)
	}
	defer dst.Close()

	srcPool := common.HexToAddress(sgEnv("SG_SRC_POOL", sgSrcPoolDefault))
	dstPool := common.HexToAddress(sgEnv("SG_DST_POOL", sgDstPoolDefault))
	dstEid := stargate.EIDEthereumSepolia

	// Resolve each side's token from the pool itself — the authoritative source.
	srcToken, err := src.Token(ctx, srcPool)
	if err != nil {
		t.Fatalf("src token: %v", err)
	}
	dstToken, err := dst.Token(ctx, dstPool)
	if err != nil {
		t.Fatalf("dst token: %v", err)
	}
	t.Logf("src pool=%s token=%s | dst pool=%s token=%s", srcPool.Hex(), srcToken.Hex(), dstPool.Hex(), dstToken.Hex())

	amount := big.NewInt(5_000_000) // 5 USDC (6 decimals, verified on both mocks)

	// Stage 1 (read-only): quote. Proves the client's ABI encode/decode against
	// the real pool even if the send stage fails later.
	quote, err := src.QuoteSend(ctx, srcPool, dstEid, addr, amount)
	if err != nil {
		t.Fatalf("quote: %v", err)
	}
	t.Logf("quote: receive=%s of %s sent, nativeFee=%s wei, pool limits [%s, %s]",
		quote.AmountReceivedLD, amount, quote.NativeFee, quote.MinAmountLD, quote.MaxAmountLD)
	if quote.AmountReceivedLD.Sign() <= 0 {
		t.Fatalf("quoteOFT returned non-positive received amount: %s", quote.AmountReceivedLD)
	}

	// Stage 2: ensure source-side test USDC (the LZ testnet mock's mint is public).
	bal, err := src.BalanceOf(ctx, srcToken, addr)
	if err != nil {
		t.Fatalf("src balance: %v", err)
	}
	if bal.Cmp(amount) < 0 {
		mintABI, err := abi.JSON(strings.NewReader(`[{"name":"mint","type":"function","inputs":[{"name":"to","type":"address"},{"name":"amount","type":"uint256"}],"outputs":[]}]`))
		if err != nil {
			t.Fatal(err)
		}
		data, err := mintABI.Pack("mint", addr, new(big.Int).Mul(amount, big.NewInt(4)))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := src.SendRaw(ctx, key, srcToken, big.NewInt(0), data); err != nil {
			t.Fatalf("mint (is mint public on %s?): %v — fund %s manually if not", srcToken.Hex(), err, addr.Hex())
		}
		bal, _ = src.BalanceOf(ctx, srcToken, addr)
		t.Logf("minted test USDC: balance now %s", bal)
	}

	// Stage 3: send (taxi) and wait for the destination credit.
	dstBefore, err := dst.BalanceOf(ctx, dstToken, addr)
	if err != nil {
		t.Fatalf("dst balance: %v", err)
	}
	txh, err := src.Send(ctx, key, srcPool, srcToken, addr, dstEid, amount, quote.AmountReceivedLD, quote.NativeFee)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	t.Logf("sent: %s (fee %s wei); waiting for delivery on dst…", txh.Hex(), quote.NativeFee)

	want := new(big.Int).Add(dstBefore, quote.AmountReceivedLD)
	got, err := dst.WaitForBalance(ctx, dstToken, addr, want, 15*time.Second, 20*time.Minute)
	if err != nil {
		t.Fatalf("delivery: %v", err)
	}
	t.Logf("delivered: dst balance %s → %s (+%s)", dstBefore, got, new(big.Int).Sub(got, dstBefore))
}
