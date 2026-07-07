package main

import (
	"encoding/json"
	"flag"
	"os"
	"strings"
	"time"

	"extension-scaffold/tools/pkg/configs"
	"extension-scaffold/tools/pkg/fccutils"
	"extension-scaffold/tools/pkg/hlinfo"
	"extension-scaffold/tools/pkg/support"
	instrutils "extension-scaffold/tools/pkg/utils"

	"github.com/ethereum/go-ethereum/common"
	"github.com/flare-foundation/go-flare-common/pkg/logger"
	"github.com/pkg/errors"
)

type depositResult struct {
	HLAddress  string  `json:"hlAddress"`
	BalanceUsd float64 `json:"balanceUsd"`
}

type withdrawResult struct {
	BalanceUsd float64 `json:"balanceUsd"`
}

func main() {
	af := flag.String("a", configs.AddressesFile, "file with deployed addresses")
	cf := flag.String("c", configs.ChainNodeURL, "chain node url")
	pf := flag.String("p", configs.ExtensionProxyURL, "extension proxy url")
	instructionSenderF := flag.String("instructionSender", "", "instructionSender address")
	hlURLf := flag.String("hlUrl", envOr("HYPERLIQUID_URL", "https://api.hyperliquid-testnet.xyz"), "hyperliquid info api url for verification")
	treasuryf := flag.String("treasury", "", "treasury HL address for a pre-send preflight (optional)")
	flag.Parse()

	instructionSenderAddress := common.HexToAddress(*instructionSenderF)
	hl := hlinfo.New(*hlURLf)

	testSupport, err := support.DefaultSupport(*af, *cf)
	if err != nil {
		fccutils.FatalWithCause(err)
	}

	// --- Generic: configure contract -----------------------------------------
	logger.Infof("Setting extension ID on instruction sender...")
	err = instrutils.SetExtensionId(testSupport, instructionSenderAddress)
	if err != nil {
		if strings.Contains(err.Error(), "already set") || strings.Contains(err.Error(), "Extension ID already set") {
			logger.Infof("Extension ID already set on contract, continuing")
		} else {
			logger.Errorf("setExtensionId failed: %s", err)
			fccutils.FatalWithCause(errors.Errorf(
				"setExtensionId failed — is the extension registered? Check that pre-build.sh completed successfully. Error: %s", err))
		}
	}

	// --- Treasury preflight (optional) --------------------------------------
	if *treasuryf != "" {
		logger.Infof("Treasury preflight for %s ...", *treasuryf)
		bal, err := hl.SpotUSDC(*treasuryf)
		if err != nil {
			fccutils.FatalWithCause(errors.Errorf("treasury preflight (spot USDC): %s", err))
		}
		open, coin, _, err := hl.OpenPerp(*treasuryf)
		if err != nil {
			fccutils.FatalWithCause(errors.Errorf("treasury preflight (positions): %s", err))
		}
		if open {
			fccutils.FatalWithCause(errors.Errorf("treasury has an open %s position — it must be flat so the extension can set standard mode; close it first", coin))
		}
		if bal < 100 {
			fccutils.FatalWithCause(errors.Errorf("treasury spot USDC %.2f is below the 100 needed to fund the deposit; seed it from the faucet", bal))
		}
		logger.Infof("treasury preflight ok: spot USDC %.2f, no open positions", bal)
	}

	// --- Step 1: DEPOSIT -----------------------------------------------------
	logger.Infof("Sending DEPOSIT instruction...")
	depPayload, _ := json.Marshal(map[string]any{"usdAmount": 100})
	depID, _, err := instrutils.SendDeposit(testSupport, instructionSenderAddress, depPayload)
	if err != nil {
		fccutils.FatalWithCause(err)
	}
	logger.Infof("DEPOSIT sent. ID: %s", depID.Hex())
	depResp, err := fccutils.ActionResult(*pf, depID)
	if err != nil {
		fccutils.FatalWithCause(err)
	}
	if depResp.Result.Status != 1 {
		fccutils.FatalWithCause(errors.Errorf("deposit failed (status=%d): %s", depResp.Result.Status, depResp.Result.Log))
	}
	logger.Infof("deposit ok: %s", string(depResp.Result.Data))

	var dep depositResult
	if err := json.Unmarshal(depResp.Result.Data, &dep); err != nil {
		fccutils.FatalWithCause(errors.Errorf("parsing deposit response: %s", err))
	}
	logger.Infof("derived HL account: %s  balanceUsd=%.2f", dep.HLAddress, dep.BalanceUsd)
	// Ledger is cumulative in memory across runs (until the extension restarts),
	// so assert the deposit credited at least its amount rather than an exact total.
	if dep.BalanceUsd < 100 {
		fccutils.FatalWithCause(errors.Errorf("expected ledger balance >= 100 after a 100 deposit, got %.2f", dep.BalanceUsd))
	}

	// --- Step 2: PERP market order -------------------------------------------
	logger.Infof("Sending PERP market order...")
	perpPayload, _ := json.Marshal(map[string]any{
		"market":    "perp",
		"coin":      "ETH",
		"isBuy":     true,
		"orderType": "market",
		"size":      0.01,
		"leverage":  3,
	})
	perpID, _, err := instrutils.SendPlaceOrder(testSupport, instructionSenderAddress, perpPayload)
	if err != nil {
		fccutils.FatalWithCause(err)
	}
	logger.Infof("PERP order sent. ID: %s", perpID.Hex())
	perpResp, err := fccutils.ActionResult(*pf, perpID)
	if err != nil {
		fccutils.FatalWithCause(err)
	}
	if perpResp.Result.Status != 1 {
		fccutils.FatalWithCause(errors.Errorf("perp order failed (status=%d): %s", perpResp.Result.Status, perpResp.Result.Log))
	}
	logger.Infof("perp order accepted: %s", string(perpResp.Result.Data))

	{
		var ok bool
		for range 5 {
			open, coin, szi, err := hl.OpenPerp(dep.HLAddress)
			if err == nil && open && coin == "ETH" {
				logger.Infof("verified perp position on HL: %s szi=%v", coin, szi)
				ok = true
				break
			}
			time.Sleep(2 * time.Second)
		}
		if !ok {
			fccutils.FatalWithCause(errors.Errorf("perp order returned ok but no ETH position visible on HL for %s", dep.HLAddress))
		}
	}

	// --- Step 3: SPOT limit order --------------------------------------------
	logger.Infof("Sending SPOT limit order...")
	spotPayload, _ := json.Marshal(map[string]any{
		"market":    "spot",
		"coin":      "PURR/USDC",
		"isBuy":     true,
		"orderType": "limit",
		"size":      20,
		"limitPx":   0.5,
	})
	spotID, _, err := instrutils.SendPlaceOrder(testSupport, instructionSenderAddress, spotPayload)
	if err != nil {
		fccutils.FatalWithCause(err)
	}
	logger.Infof("SPOT order sent. ID: %s", spotID.Hex())
	spotResp, err := fccutils.ActionResult(*pf, spotID)
	if err != nil {
		fccutils.FatalWithCause(err)
	}
	if spotResp.Result.Status != 1 {
		fccutils.FatalWithCause(errors.Errorf("spot order failed (status=%d): %s", spotResp.Result.Status, spotResp.Result.Log))
	}
	logger.Infof("spot order accepted: %s", string(spotResp.Result.Data))

	// --- Step 4: WITHDRAW ----------------------------------------------------
	logger.Infof("Sending WITHDRAW instruction...")
	wdPayload, _ := json.Marshal(map[string]any{"usdAmount": 20})
	wdID, _, err := instrutils.SendWithdraw(testSupport, instructionSenderAddress, wdPayload)
	if err != nil {
		fccutils.FatalWithCause(err)
	}
	logger.Infof("WITHDRAW sent. ID: %s", wdID.Hex())
	wdResp, err := fccutils.ActionResult(*pf, wdID)
	if err != nil {
		fccutils.FatalWithCause(err)
	}
	if wdResp.Result.Status != 1 {
		fccutils.FatalWithCause(errors.Errorf("withdraw failed (status=%d): %s", wdResp.Result.Status, wdResp.Result.Log))
	}
	logger.Infof("withdraw ok: %s", string(wdResp.Result.Data))

	var wd withdrawResult
	if err := json.Unmarshal(wdResp.Result.Data, &wd); err != nil {
		fccutils.FatalWithCause(errors.Errorf("parsing withdraw response: %s", err))
	}
	if wd.BalanceUsd != dep.BalanceUsd-20 {
		fccutils.FatalWithCause(errors.Errorf("expected balance %.2f after withdraw, got %.2f", dep.BalanceUsd-20, wd.BalanceUsd))
	}
	logger.Infof("verified ledger balance after withdraw: %.2f", wd.BalanceUsd)

	logger.Infof("All tests passed.")
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
