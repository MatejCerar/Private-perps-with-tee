package config

import (
	"os"
	"testing"
)

func TestLoadHLConfigReadsEnv(t *testing.T) {
	os.Setenv("HYPERLIQUID_URL", "https://api.hyperliquid-testnet.xyz")
	os.Setenv("HL_MASTER_SEED", "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff")
	os.Setenv("HL_TREASURY_KEY", "1111111111111111111111111111111111111111111111111111111111111111")
	os.Setenv("HL_MAX_DEPOSIT_USD", "250")
	defer func() {
		os.Unsetenv("HYPERLIQUID_URL")
		os.Unsetenv("HL_MASTER_SEED")
		os.Unsetenv("HL_TREASURY_KEY")
		os.Unsetenv("HL_MAX_DEPOSIT_USD")
	}()

	if err := LoadHLConfig(); err != nil {
		t.Fatalf("LoadHLConfig: %v", err)
	}
	if HyperliquidURL != "https://api.hyperliquid-testnet.xyz" {
		t.Errorf("url = %q", HyperliquidURL)
	}
	if MasterSeedHex == "" || TreasuryKeyHex == "" {
		t.Errorf("seed/treasury not loaded")
	}
	if MaxDepositUSD != 250 {
		t.Errorf("MaxDepositUSD = %v", MaxDepositUSD)
	}
}

func TestLoadHLConfigRequiresSecrets(t *testing.T) {
	os.Unsetenv("HL_MASTER_SEED")
	os.Unsetenv("HL_TREASURY_KEY")
	if err := LoadHLConfig(); err == nil {
		t.Fatal("expected error when secrets missing")
	}
}
