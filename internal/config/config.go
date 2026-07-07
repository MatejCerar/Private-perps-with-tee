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

// Arbitrum / bridge settings for the Hyperliquid funding path (populated by
// LoadArbConfig). Defaults target Arbitrum Sepolia and the HL testnet Bridge2.
// These deposit/withdraw funds via the real HL bridge instead of the legacy
// treasury spotSend path.
var (
	ArbitrumRPCURL   = "https://sepolia-rollup.arbitrum.io/rpc"
	ArbUSDCAddress   = "0x1baAbB04529D43a73232B713C0FE471f7c7334d5" // HL testnet mock USDC (Arbitrum Sepolia)
	HLBridge2Address = "0x08cfc1B6b2dCF36A1480b99353A354AA8AC56f89" // HL testnet Bridge2 (Arbitrum Sepolia)
	ArbReserveKeyHex string                                         // reserve key; defaults to HL_TREASURY_KEY

	// MinDepositUSD is the HL Bridge2 minimum deposit. Deposits below this are
	// not credited and are lost forever, so the handler rejects them upstream.
	MinDepositUSD = 5.0
	// ReserveAutoRefill enables topping up the reserve's Arbitrum USDC from its
	// HL balance (spot->perp, then withdraw3) when it dips below ReserveRefillFloorUSD.
	// Off by default so behavior is explicit; the operator can enable it once the
	// reserve holds a buffer on the HL side.
	ReserveAutoRefill    = false
	ReserveRefillFloorUSD = 50.0

	// ReconcileToleranceUSD is the drift below which reconciliation leaves the
	// ledger untouched (float dust). The ~1 USDC withdraw3 fee is well above it
	// and so reconciles. ReconcileIntervalSeconds is the background tick cadence;
	// 0 disables the background reconciler (on-demand Reconcile still works).
	ReconcileToleranceUSD   = 0.01
	ReconcileIntervalSeconds = 120
)

// FundingMode selects how DEPOSIT/WITHDRAW move funds: "treasury" (legacy
// spotSend from a shared HL treasury) or "bridge" (real Arbitrum Bridge2
// deposit + withdraw3). Defaults to "treasury" for backward compatibility.
var FundingMode = "treasury"

// LoadArbConfig reads Arbitrum-side settings for the bridge funding path.
// Optional: the legacy treasury/spotSend deposit path does not use these.
// ARB_RESERVE_KEY defaults to HL_TREASURY_KEY so the faucet-funded treasury can
// double as the Arbitrum reserve on testnet.
func LoadArbConfig() {
	if v := os.Getenv("ARBITRUM_RPC_URL"); v != "" {
		ArbitrumRPCURL = v
	}
	if v := os.Getenv("ARB_USDC_ADDRESS"); v != "" {
		ArbUSDCAddress = v
	}
	if v := os.Getenv("HL_BRIDGE2_ADDRESS"); v != "" {
		HLBridge2Address = v
	}
	if ArbReserveKeyHex = os.Getenv("ARB_RESERVE_KEY"); ArbReserveKeyHex == "" {
		ArbReserveKeyHex = os.Getenv("HL_TREASURY_KEY")
	}
	if v := os.Getenv("HL_FUNDING_MODE"); v != "" {
		FundingMode = v
	}
	if v := os.Getenv("HL_MIN_DEPOSIT_USD"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			MinDepositUSD = f
		}
	}
	if v := os.Getenv("HL_RESERVE_AUTO_REFILL"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			ReserveAutoRefill = b
		}
	}
	if v := os.Getenv("HL_RESERVE_REFILL_FLOOR_USD"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			ReserveRefillFloorUSD = f
		}
	}
	if v := os.Getenv("HL_RECONCILE_TOLERANCE_USD"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			ReconcileToleranceUSD = f
		}
	}
	if v := os.Getenv("HL_RECONCILE_INTERVAL_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			ReconcileIntervalSeconds = n
		}
	}
}

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
	ExtensionPort   = 8080
	SignPort        = 9090
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
