//go:build hltestnet

package hl

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"extension-scaffold/internal/keys"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

const teeHLTestnetURL = "https://api.hyperliquid-testnet.xyz"

// TestTeeWalletOrderLive proves the full tee-wallet (PMW / C9) custody path
// end-to-end against HL testnet: a key sealed inside the TEE — reached only over
// the sign server, never exported — signs real HL actions that HL accepts. The
// test holds no private key for the trading account; every state-changing action
// (SetAbstraction, order, cancel) is signed by the enclave.
//
// Requires:
//   - HL_SIGN_URL: the enclave sign server, reachable from the host. Default
//     http://127.0.0.1:7701 — publish the in-container server first, e.g.:
//     docker run -d --rm --name c9-signproxy -p 127.0.0.1:7701:7701 \
//       --network extension-scaffold-coston alpine/socat \
//       TCP-LISTEN:7701,fork,reuseaddr TCP:hype-accounts-extension-tee-1:7701
//   - HL_WALLET_REGISTRY: the provisioned registry (default ../../config/wallet-registry.json).
//   - HL_TEE_USER: the Flare user mapped in that registry (default the deployer).
//   - HL_TREASURY_KEY: a treasury holding HL testnet spot USDC (funds the account).
func TestTeeWalletOrderLive(t *testing.T) {
	signURL := envOr("HL_SIGN_URL", "http://127.0.0.1:7701")
	regPath := envOr("HL_WALLET_REGISTRY", "../../config/wallet-registry.json")
	userHex := envOr("HL_TEE_USER", "0xDa50A19aF65655785ab1F4c6a5b0592AFC497C88")
	treasuryHex := os.Getenv("HL_TREASURY_KEY")
	if treasuryHex == "" {
		t.Skip("set HL_TREASURY_KEY (treasury funded with HL testnet spot USDC)")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// 1. Resolve the tee-wallet signer from the registry + enclave sign server.
	reg, err := keys.LoadFileRegistry(regPath)
	if err != nil {
		t.Fatalf("load registry %s: %v", regPath, err)
	}
	user := common.HexToAddress(userHex)
	if _, _, ok := reg.Lookup(user); !ok {
		t.Fatalf("no wallet provisioned for %s in %s (run tools/cmd/provision-wallet)", user.Hex(), regPath)
	}
	signer, err := keys.NewTeeWalletKeyer(signURL, reg).UserSigner(user)
	if err != nil {
		t.Fatalf("resolving tee-wallet signer (is the sign server reachable at %s?): %v", signURL, err)
	}
	hlAddr := signer.Address().Hex()
	t.Logf("tee-wallet HL account %s — key sealed in the TEE, resolved via %s", hlAddr, signURL)

	// 2. Full HL account whose writes are enclave-signed (no raw key here).
	teeAcct := NewTeeWalletFactory(teeHLTestnetURL).AccountForSigner(signer)
	if !strings.EqualFold(teeAcct.Address(), hlAddr) {
		t.Fatalf("account address %s != signer address %s", teeAcct.Address(), hlAddr)
	}

	// 3. Fund from the treasury (raw-key spotSend lands in the account's spot).
	treasuryKey, err := crypto.HexToECDSA(strings.TrimPrefix(treasuryHex, "0x"))
	if err != nil {
		t.Fatalf("treasury key: %v", err)
	}
	treasuryAddr := crypto.PubkeyToAddress(treasuryKey.PublicKey).Hex()
	treasury := NewRealFactory(teeHLTestnetURL).Account(treasuryKey, treasuryAddr)

	const fundUSD = 20.0
	startSpot, _ := teeAcct.SpotUSDC(ctx)
	if startSpot < fundUSD*0.9 {
		if err := treasury.SendUSD(ctx, hlAddr, fundUSD); err != nil {
			t.Fatalf("treasury spotSend %.2f -> %s: %v", fundUSD, hlAddr, err)
		}
		t.Logf("treasury %s spotSent %.2f USDC -> %s; waiting for credit", treasuryAddr, fundUSD, hlAddr)
		waitForSpot(ctx, t, teeAcct, startSpot+fundUSD*0.9)
	}
	spot, _ := teeAcct.SpotUSDC(ctx)
	t.Logf("tee account spot USDC: %.4f", spot)

	// 4. Enclave-signed USER action: unify spot+perp so a perp order draws from the
	//    single pool (and proves HL accepts a tee-wallet user-signed action).
	if err := teeAcct.SetAbstraction(ctx, AbstractionUnified); err != nil {
		t.Fatalf("SetAbstraction(unified) [tee-wallet user-signed] rejected: %v", err)
	}
	t.Log("SetAbstraction(unified) accepted — enclave user-signed action OK")

	// 5. Enclave-signed L1 action: a perp buy limit at ~half the ETH mid rests on
	//    the book (never fills; within HL's 80%-from-reference cap; above ~$10 min).
	mid := allMids(ctx, t)["ETH"]
	if mid < 2 {
		t.Fatalf("implausible ETH mid %.4f", mid)
	}
	px := float64(int(mid * 0.5))
	size := math.Ceil((12.0/px)*1000) / 1000 // ~$12 notional
	ord, err := teeAcct.PlaceOrder(ctx, OrderReq{
		Market: "perp", Coin: "ETH", IsBuy: true, OrderType: "limit", Size: size, LimitPx: px,
	})
	if err != nil {
		t.Fatalf("PlaceOrder [tee-wallet L1-signed] rejected: %v", err)
	}
	if ord.Status != "resting" || ord.OrderID == 0 {
		t.Fatalf("expected a resting order; got status=%q oid=%d raw=%s", ord.Status, ord.OrderID, string(ord.Raw))
	}
	t.Logf("HL ACCEPTED tee-wallet-signed order: oid=%d resting px=%.2f size=%.4f", ord.OrderID, px, size)

	// 6. Confirm HL lists it as a live open order, then cancel it (L1-signed).
	if !openOrderExists(ctx, t, hlAddr, ord.OrderID) {
		t.Fatalf("order %d not found in HL open orders", ord.OrderID)
	}
	if err := teeAcct.Cancel(ctx, "ETH", ord.OrderID); err != nil {
		t.Fatalf("Cancel [tee-wallet L1-signed] rejected: %v", err)
	}
	if openOrderExists(ctx, t, hlAddr, ord.OrderID) {
		t.Fatalf("order %d still open after cancel", ord.OrderID)
	}
	t.Logf("cancel accepted; order %d removed. Full tee-wallet custody loop proven live on HL.", ord.OrderID)
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// waitForSpot polls the account's spot USDC until it reaches target (or times out).
func waitForSpot(ctx context.Context, t *testing.T, acct Account, target float64) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		if bal, _ := acct.SpotUSDC(ctx); bal >= target {
			return
		}
		time.Sleep(3 * time.Second)
	}
	bal, _ := acct.SpotUSDC(ctx)
	t.Fatalf("spot USDC did not reach %.2f within 90s (last %.4f)", target, bal)
}

// allMids fetches current mid prices from HL testnet (/info allMids).
func allMids(ctx context.Context, t *testing.T) map[string]float64 {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"type": "allMids"})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, teeHLTestnetURL+"/info", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("allMids: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("allMids decode: %v (%s)", err, string(raw))
	}
	out := make(map[string]float64, len(m))
	for k, v := range m {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			out[k] = f
		}
	}
	return out
}

// openOrderExists reports whether oid is among the account's HL open orders.
func openOrderExists(ctx context.Context, t *testing.T, addr string, oid int64) bool {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"type": "openOrders", "user": addr})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, teeHLTestnetURL+"/info", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("openOrders: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var orders []struct {
		Oid int64 `json:"oid"`
	}
	if err := json.Unmarshal(raw, &orders); err != nil {
		t.Fatalf("openOrders decode: %v (%s)", err, string(raw))
	}
	for _, o := range orders {
		if o.Oid == oid {
			return true
		}
	}
	return false
}
