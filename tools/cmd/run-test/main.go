package main

import (
	"encoding/json"
	"flag"
	"strings"

	"extension-scaffold/tools/pkg/configs"
	"extension-scaffold/tools/pkg/fccutils"
	"extension-scaffold/tools/pkg/support"
	instrutils "extension-scaffold/tools/pkg/utils"

	"github.com/ethereum/go-ethereum/common"
	"github.com/flare-foundation/go-flare-common/pkg/logger"
	"github.com/pkg/errors"
)

func main() {
	af := flag.String("a", configs.AddressesFile, "file with deployed addresses")
	cf := flag.String("c", configs.ChainNodeURL, "chain node url")
	pf := flag.String("p", configs.ExtensionProxyURL, "extension proxy url")
	instructionSenderF := flag.String("instructionSender", "", "instructionSender address")
	flag.Parse()

	instructionSenderAddress := common.HexToAddress(*instructionSenderF)

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

	logger.Infof("All tests passed.")
}
