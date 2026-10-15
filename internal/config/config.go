// Package config contains configuration values and defaults used by the extension.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	Version = "0.1.0"

	OPTypeHL = "HL"

	OPCommandDeposit        = "DEPOSIT"
	OPCommandPlaceOrder     = "PLACE_ORDER"
	OPCommandCancel         = "CANCEL"
	OPCommandWithdraw       = "WITHDRAW"
	OPCommandConfirmFunding = "CONFIRM_FUNDING"

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
// spotSend from a shared HL treasury), "bridge" (real per-wallet Arbitrum Bridge2
// deposit + withdraw3), or "vault" (Coston TestUSDC vault front + central Bridge2
// to the treasury + spotSend to users; the Coston↔Arbitrum hop is skipped).
// Defaults to "treasury" for backward compatibility.
var FundingMode = "treasury"

// Vault-mode settings (populated by LoadArbConfig).
var (
	// VaultAddress is the Coston Vault contract that escrows deposited TestUSDC.
	VaultAddress string
	// VaultAuthorityKeyHex signs the withdrawal slips the Vault verifies. Defaults
	// to HL_TREASURY_KEY (the vault's setTeeAuthority is set to this key's address).
	VaultAuthorityKeyHex string
	// Bridge2Mock skips the real Arbitrum↔HL Bridge2 legs (deposit + withdraw3) and
	// funds users straight from the pre-funded treasury via spotSend. Defaults to
	// true so the full round-trip works without a funded Arbitrum Sepolia reserve;
	// set HL_BRIDGE2_MOCK=false once the reserve holds testnet USDC + gas.
	Bridge2Mock = true
)

// KeyMode selects HL account key custody: "seed" (per-user keys derived from
// HL_MASTER_SEED, in-process) or "tee-wallet" (keys sealed in the TEE, reached
// over the sign server — PMW custody, C9). Defaults to "seed".
var KeyMode = "seed"

// SignURL is the tee-node extension sign server (localhost, SIGN_PORT) used in
// tee-wallet mode for /sign and /key-info.
var SignURL = "http://127.0.0.1:7701"

// WalletRegistryPath is the JSON file mapping users to their provisioned TEE
// wallet (walletID, keyID), written by tools/cmd/provision-wallet.
var WalletRegistryPath = "./config/wallet-registry.json"

// --- Private deposit (delayed, batched, fixed-denomination funding) ---
//
// The account model is unchanged (the per-user HL account is still seed-derived,
// Y = HKDF(masterSeed, user); the master seed never leaves the TEE, so an outsider
// cannot derive Y from the Flare address). What this adds is UNLINKABLE FUNDING:
// instead of funding Y synchronously on the DEPOSIT instruction (which an observer
// could correlate to the on-chain deposit by amount + time), the TEE escrows the
// deposit and funds Y later, from the shared treasury, after a randomized delay,
// batched with other deposits, in a fixed denomination. So no observer can match a
// Flare deposit to the HL account that gets funded.
var (
	// PrivateDeposit turns the delayed/batched funding path on. Off by default so
	// the original synchronous behaviour is preserved unless explicitly enabled.
	PrivateDeposit = false

	// Denominations are the only deposit amounts accepted when PrivateDeposit is on
	// (USD). Uniform amounts stop an observer re-linking a user by matching the
	// on-chain deposit amount to the HL funding amount. Empty disables the check.
	Denominations = []float64{25, 100, 500}

	// BatchMin is the minimum number of matured deposits funded together (the
	// anonymity set). 1 = no anonymity; raise it for real privacy.
	BatchMin = 1
	// FundDelayMinSeconds/FundDelayMaxSeconds bound the randomized per-deposit delay
	// before its HL account is funded, decorrelating funding from the deposit.
	FundDelayMinSeconds = 20
	FundDelayMaxSeconds = 60
	// FundTickSeconds is how often the batch funder wakes to drain due deposits.
	FundTickSeconds = 5
	// MaxFundingAgeSeconds drops a pending deposit/withdraw whose funding kept
	// failing for this long (e.g. the treasury could not fund it), so an
	// un-fundable entry never blocks the account forever. 0 disables expiry.
	MaxFundingAgeSeconds = 120
)

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
	if v := os.Getenv("HL_KEY_MODE"); v != "" {
		KeyMode = v
	}
	if v := os.Getenv("HL_SIGN_URL"); v != "" {
		SignURL = v
	}
	if v := os.Getenv("HL_WALLET_REGISTRY"); v != "" {
		WalletRegistryPath = v
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
	// vault-mode settings
	VaultAddress = os.Getenv("VAULT_ADDRESS")
	if VaultAuthorityKeyHex = os.Getenv("HL_VAULT_AUTHORITY_KEY"); VaultAuthorityKeyHex == "" {
		VaultAuthorityKeyHex = os.Getenv("HL_TREASURY_KEY")
	}
	if v := os.Getenv("HL_BRIDGE2_MOCK"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			Bridge2Mock = b
		}
	}
	// private-deposit settings
	if v := os.Getenv("HL_PRIVATE_DEPOSIT"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			PrivateDeposit = b
		}
	}
	if v := os.Getenv("HL_DENOMINATIONS"); v != "" {
		Denominations = parseFloatCSV(v)
	}
	if v := os.Getenv("HL_BATCH_MIN"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			BatchMin = n
		}
	}
	if v := os.Getenv("HL_FUND_DELAY_MIN_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			FundDelayMinSeconds = n
		}
	}
	if v := os.Getenv("HL_FUND_DELAY_MAX_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			FundDelayMaxSeconds = n
		}
	}
	if v := os.Getenv("HL_FUND_TICK_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			FundTickSeconds = n
		}
	}
	if v := os.Getenv("HL_MAX_FUNDING_AGE_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			MaxFundingAgeSeconds = n
		}
	}
}

// parseFloatCSV parses "25,100,500" into []float64, skipping blanks/invalids.
func parseFloatCSV(s string) []float64 {
	var out []float64
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if f, err := strconv.ParseFloat(part, 64); err == nil && f > 0 {
			out = append(out, f)
		}
	}
	return out
}

// IsDenomination reports whether usd matches an allowed deposit denomination
// (within a cent). Always true when no denominations are configured.
func IsDenomination(usd float64) bool {
	if len(Denominations) == 0 {
		return true
	}
	for _, d := range Denominations {
		if usd >= d-0.01 && usd <= d+0.01 {
			return true
		}
	}
	return false
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
