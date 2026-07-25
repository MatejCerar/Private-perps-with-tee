// deploy-vault: deploy the Coston TestUSDC token + Vault, allow-list the token,
// and wire the Vault into the already-deployed InstructionSender (setVault).
// The Vault's TEE authority is set separately, post-TEE-registration, with
// -teeAuthority (or via a later call to VaultSetTeeAuthority).
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"extension-scaffold/tools/pkg/configs"
	"extension-scaffold/tools/pkg/fccutils"
	"extension-scaffold/tools/pkg/support"
	instrutils "extension-scaffold/tools/pkg/utils"
	"extension-scaffold/tools/pkg/validate"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/flare-foundation/go-flare-common/pkg/logger"
)

// resolveTeeAuthority returns the vault authority address: the -teeAuthority flag
// if set, else the address of HL_VAULT_AUTHORITY_KEY, else HL_TREASURY_KEY (which
// the extension also signs slips with by default).
func resolveTeeAuthority(flagVal string) (common.Address, bool) {
	if flagVal != "" {
		return common.HexToAddress(flagVal), true
	}
	kh := os.Getenv("HL_VAULT_AUTHORITY_KEY")
	if kh == "" {
		kh = os.Getenv("HL_TREASURY_KEY")
	}
	if kh == "" {
		return common.Address{}, false
	}
	k, err := crypto.HexToECDSA(strings.TrimPrefix(kh, "0x"))
	if err != nil {
		return common.Address{}, false
	}
	return crypto.PubkeyToAddress(k.PublicKey), true
}

func main() {
	af := flag.String("a", configs.AddressesFile, "file with deployed addresses")
	cf := flag.String("c", configs.ChainNodeURL, "chain node url")
	instructionSenderF := flag.String("instructionSender", "", "deployed InstructionSender address (to setVault)")
	teeAuthorityF := flag.String("teeAuthority", "", "TEE authority address to record on the Vault (optional; can be set later)")
	flag.Parse()

	s, err := support.DefaultSupport(*af, *cf)
	if err != nil {
		fccutils.FatalWithCause(err)
	}
	if err := validate.KeyHasFunds(s.ChainClient, s.Prv, validate.MinDeployBalance); err != nil {
		fccutils.FatalWithCause(err)
	}
	owner := crypto.PubkeyToAddress(s.Prv.PublicKey)

	logger.Infof("Deploying TestUSDC...")
	token, err := instrutils.DeployTestUSDC(s)
	if err != nil {
		fccutils.FatalWithCause(err)
	}
	logger.Infof("TestUSDC deployed at: %s", token.Hex())

	logger.Infof("Deploying Vault (owner %s)...", owner.Hex())
	vaultAddr, err := instrutils.DeployVault(s, owner)
	if err != nil {
		fccutils.FatalWithCause(err)
	}
	logger.Infof("Vault deployed at: %s", vaultAddr.Hex())

	logger.Infof("Allow-listing TestUSDC on the Vault...")
	if err := instrutils.VaultSetAllowedToken(s, vaultAddr, token, true); err != nil {
		fccutils.FatalWithCause(err)
	}

	if *instructionSenderF != "" {
		sender := common.HexToAddress(*instructionSenderF)
		logger.Infof("Wiring Vault into InstructionSender %s (setVault)...", sender.Hex())
		if err := instrutils.SetVault(s, sender, vaultAddr); err != nil {
			fccutils.FatalWithCause(err)
		}
	} else {
		logger.Warnf("no -instructionSender given: run setVault(%s) manually once it's deployed", vaultAddr.Hex())
	}

	if tee, ok := resolveTeeAuthority(*teeAuthorityF); ok {
		logger.Infof("Recording TEE authority %s on the Vault...", tee.Hex())
		if err := instrutils.VaultSetTeeAuthority(s, vaultAddr, tee); err != nil {
			fccutils.FatalWithCause(err)
		}
	} else {
		logger.Warnf("no TEE authority resolved (set -teeAuthority or HL_VAULT_AUTHORITY_KEY/HL_TREASURY_KEY): call vault.setTeeAuthority(<addr>) later")
	}

	// Machine-readable output for scripts / .env wiring.
	fmt.Printf("TESTUSDC_ADDRESS=%s\n", token.Hex())
	fmt.Printf("VAULT_ADDRESS=%s\n", vaultAddr.Hex())
}
