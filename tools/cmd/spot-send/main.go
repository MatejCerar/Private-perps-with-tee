// spot-send: send USDC from the treasury HL account to an arbitrary address.
// Usage: HL_TREASURY_KEY=<hex> go run ./tools/cmd/spot-send -to 0x... -amount 10
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"extension-scaffold/internal/hl"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/joho/godotenv"
)

func main() {
	_ = godotenv.Load()

	to := flag.String("to", "", "destination HL address (0x...)")
	amount := flag.Float64("amount", 0, "USDC amount to send")
	flag.Parse()

	if *to == "" || *amount <= 0 {
		fmt.Fprintln(os.Stderr, "usage: spot-send -to 0x... -amount <usd>")
		os.Exit(1)
	}

	keyHex := os.Getenv("HL_TREASURY_KEY")
	if keyHex == "" {
		fmt.Fprintln(os.Stderr, "HL_TREASURY_KEY not set")
		os.Exit(1)
	}
	key, err := crypto.HexToECDSA(strings.TrimPrefix(keyHex, "0x"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "treasury key: %v\n", err)
		os.Exit(1)
	}
	treasuryAddr := crypto.PubkeyToAddress(key.PublicKey).Hex()

	hlURL := os.Getenv("HYPERLIQUID_URL")
	if hlURL == "" {
		hlURL = "https://api.hyperliquid-testnet.xyz"
	}

	factory := hl.NewRealFactory(hlURL)
	treasury := factory.Account(key, treasuryAddr)

	fmt.Printf("treasury: %s\n", treasuryAddr)
	fmt.Printf("sending %.2f USDC -> %s\n", *amount, *to)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := treasury.SendUSD(ctx, *to, *amount); err != nil {
		fmt.Fprintf(os.Stderr, "spotSend failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("spotSend OK")
}
