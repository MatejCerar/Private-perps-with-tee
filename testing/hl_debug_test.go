//go:build hltestnet

package testing

import (
	"bytes"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	"extension-scaffold/internal/keys"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
	gohl "github.com/sonirico/go-hyperliquid"
)

// TestSpotSendRaw performs a raw signed spotSend (exactly like the library) but
// prints Hyperliquid's FULL response body, so we can see the real rejection
// reason that go-hyperliquid's TransferResponse drops (HL puts it in "response").
//
//	HL_TREASURY_KEY=... HL_MASTER_SEED=... go test -tags hltestnet ./testing/ -run TestSpotSendRaw -v
func TestSpotSendRaw(t *testing.T) {
	tkHex := os.Getenv("HL_TREASURY_KEY")
	seedHex := os.Getenv("HL_MASTER_SEED")
	if tkHex == "" || seedHex == "" {
		t.Skip("set HL_TREASURY_KEY and HL_MASTER_SEED")
	}
	tk, err := crypto.HexToECDSA(tkHex)
	if err != nil {
		t.Fatal(err)
	}
	seed, err := keys.MustHexSeed(seedHex)
	if err != nil {
		t.Fatal(err)
	}
	uk, _ := keys.DeriveUserKey(seed, common.HexToAddress("0x000000000000000000000000000000000000dEaD"))
	dest := crypto.PubkeyToAddress(uk.PublicKey).Hex()

	const baseURL = "https://api.hyperliquid-testnet.xyz"
	nonce := time.Now().UnixMilli()
	action := map[string]any{
		"destination": dest,
		"amount":      strconv.FormatFloat(5, 'f', -1, 64),
		"token":       "USDC:0xeb62eee3685fc4c43992febcd9e75443",
		"time":        big.NewInt(nonce),
		"type":        "spotSend",
	}
	payloadTypes := []apitypes.Type{
		{Name: "hyperliquidChain", Type: "string"},
		{Name: "destination", Type: "string"},
		{Name: "token", Type: "string"},
		{Name: "amount", Type: "string"},
		{Name: "time", Type: "uint64"},
	}
	// Signs and mutates `action` (adds hyperliquidChain/signatureChainId), then
	// we post the same mutated action — mirroring the library's postAction.
	sig, err := gohl.SignUserSignedAction(tk, action, payloadTypes, "HyperliquidTransaction:SpotSend", false)
	if err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{"action": action, "nonce": nonce, "signature": sig}
	b, _ := json.Marshal(payload)

	resp, err := http.Post(baseURL+"/exchange", "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	t.Logf("destination (fresh derived acct): %s", dest)
	t.Logf("HL raw /exchange response: %s", string(body))
}
