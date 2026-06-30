// Package config contains configuration values and defaults used by the extension.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

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

// Defaults.
var (
	ExtensionPort  = 8080
	SignPort       = 9090
	TypesServerPort = 8100
)

// Environment variables override defaults.
func init() {
	ep := os.Getenv("EXTENSION_PORT")
	sp := os.Getenv("SIGN_PORT")
	tp := os.Getenv("TYPES_SERVER_PORT")

	if ep != "" {
		if v, err := strconv.Atoi(ep); err == nil {
			ExtensionPort = v
		}
	}
	if sp != "" {
		if v, err := strconv.Atoi(sp); err == nil {
			SignPort = v
		}
	}
	if tp != "" {
		if v, err := strconv.Atoi(tp); err == nil {
			TypesServerPort = v
		}
	}
}
