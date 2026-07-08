// Command provision-wallet creates a TEE-custodied EVM signing key for a user
// and records it in the wallet registry the extension reads in tee-wallet mode
// (HL_KEY_MODE=tee-wallet). This closes the on-chain half of C9 (PMW custody):
// the signing code path is already proven byte-identical to the seed path; this
// tool makes the key actually exist inside the enclave.
//
// Flow (each letter of -command gates a step, always run in this order):
//
//	p  createProject(extensionID, EVM type, EVM sign algo)
//	w  createWallet(projectID)
//	a  setAdmins(adminPubKeys, threshold) + each admin confirmAdmin  (admins gas-funded first)
//	c  closeWalletInitialization
//	t  setMultisigThreshold(1)            (single-TEE wallet: the TEE signs alone)
//	k  addKey(teeID, walletID)            -> keyID (TEE generates the key)
//	f  fetch existence proof from proxy + confirmKey
//	e  enableWallet
//
// Then it derives the key's EVM/HL address from the on-chain public key and
// writes user -> (walletID, keyID) into the registry file.
//
// Resume a partial run by dropping already-done letters and passing -walletID
// (and -projectID if "p" is skipped), mirroring tee/e2e's cmd/wallet.
package main

import (
	"context"
	"crypto/ecdsa"
	"extension-scaffold/internal/keys"
	"extension-scaffold/tools/pkg/configs"
	"extension-scaffold/tools/pkg/fccutils"
	"extension-scaffold/tools/pkg/support"
	"flag"
	"fmt"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/flare-foundation/go-flare-common/pkg/contracts/tee/walletmanager"
	"github.com/flare-foundation/go-flare-common/pkg/logger"
	teetypes "github.com/flare-foundation/tee-node/pkg/types"
	"github.com/flare-foundation/tee-node/pkg/wallets"
	"github.com/joho/godotenv"
)

func main() {
	af := flag.String("a", configs.AddressesFile, "file with deployed addresses")
	cf := flag.String("c", configs.ChainNodeURL, "chain node url")
	pf := flag.String("p", "http://localhost:6674", "extension proxy url (TEE info + wallet key proof)")
	userF := flag.String("user", "", "Flare user address to map to this wallet (defaults to the deployer address)")
	extF := flag.String("e", "", "extension ID (hex, e.g. 0x10037); defaults to EXTENSION_ID env")
	thresholdF := flag.Uint64("threshold", 0, "admin restore threshold; defaults to HL_WALLET_ADMIN_THRESHOLD or ceil(n/2)+ (2-of-3)")
	registryF := flag.String("registry", "../config/wallet-registry.json", "wallet registry file to write (user -> walletId,keyId)")
	fundF := flag.String("fund", "500000000000000000", "wei of native gas to top each admin up to before confirmAdmin (0 to skip)")
	command := flag.String("command", "pwactkfe", "steps to run (subset of pwactkfe)")
	walletF := flag.String("walletID", "", "walletID (required if 'p'/'w' skipped)")
	projectF := flag.String("projectID", "", "projectID (required if 'p' skipped but 'w' set)")
	flag.Parse()

	if err := godotenv.Load(); err != nil {
		logger.Warnf("no .env loaded: %v", err)
	}

	s, err := support.DefaultSupport(*af, *cf)
	if err != nil {
		fccutils.FatalWithCause(err)
	}
	deployer := crypto.PubkeyToAddress(s.Prv.PublicKey)
	logger.Infof("deployer/owner: %s (chainID %s)", deployer.Hex(), s.ChainID)

	user := deployer
	if *userF != "" {
		user = common.HexToAddress(*userF)
	}

	extensionID, err := parseExtensionID(*extF)
	if err != nil {
		fccutils.FatalWithCause(err)
	}
	logger.Infof("extension ID: %s (0x%x)", extensionID, extensionID)

	adminKeys, err := loadAdminKeys()
	if err != nil {
		fccutils.FatalWithCause(err)
	}
	threshold := *thresholdF
	if threshold == 0 {
		threshold = defaultAdminThreshold(len(adminKeys))
	}
	if threshold > uint64(len(adminKeys)) {
		fccutils.FatalWithCause(errorf("threshold %d exceeds admin count %d", threshold, len(adminKeys)))
	}
	logger.Infof("admins: %d, restore threshold: %d", len(adminKeys), threshold)

	teeID, _, err := fccutils.GetTeeProxyID(*pf)
	if err != nil {
		fccutils.FatalWithCause(errorf("querying TEE id from proxy %s: %s", *pf, err))
	}
	logger.Infof("target TEE: %s", teeID.Hex())

	keyType := wallets.EVMType
	signingAlgo := wallets.EVMSignAlgo

	// Ensure the extension allows EVM keys (idempotent).
	if supported, err := fccutils.IsKeyTypeSupported(s, extensionID, keyType); err != nil {
		fccutils.FatalWithCause(err)
	} else if !supported {
		logger.Infof("adding EVM key type support to extension %s", extensionID)
		if err := fccutils.AddSupportedKeyTypes(s, extensionID, []common.Hash{keyType}); err != nil {
			fccutils.FatalWithCause(err)
		}
	} else {
		logger.Info("EVM key type already supported")
	}

	cmd := *command
	var projectID, walletID common.Hash

	// p: create project
	if strings.Contains(cmd, "p") {
		projectID, err = fccutils.CreateProject(s, ownerOpts(s), extensionID, keyType, signingAlgo)
		if err != nil {
			fccutils.FatalWithCause(err)
		}
		logger.Infof("project created: %s", projectID.Hex())
	} else if *projectF != "" {
		projectID = common.HexToHash(*projectF)
	}

	// w: create wallet
	if strings.Contains(cmd, "w") {
		if (projectID == common.Hash{}) {
			fccutils.FatalWithCause(errorf("createWallet needs a projectID (pass -projectID or include 'p')"))
		}
		walletID, err = fccutils.CreateWallet(s, ownerOpts(s), projectID)
		if err != nil {
			fccutils.FatalWithCause(err)
		}
		logger.Infof("wallet created: %s", walletID.Hex())
	} else {
		if *walletF == "" {
			fccutils.FatalWithCause(errorf("no 'w' step: -walletID is required"))
		}
		walletID = common.HexToHash(*walletF)
	}

	// a: set + confirm admins (fund admin gas first)
	if strings.Contains(cmd, "a") {
		fundWei, ok := new(big.Int).SetString(*fundF, 10)
		if !ok {
			fccutils.FatalWithCause(errorf("invalid -fund value %q", *fundF))
		}
		if fundWei.Sign() > 0 {
			for _, k := range adminKeys {
				if err := fundGasUpTo(s, crypto.PubkeyToAddress(k.PublicKey), fundWei); err != nil {
					fccutils.FatalWithCause(err)
				}
			}
		}

		pubKeys := make([]walletmanager.PublicKey, len(adminKeys))
		for i, k := range adminKeys {
			p := teetypes.PubKeyToStruct(&k.PublicKey)
			pubKeys[i] = walletmanager.PublicKey{X: p.X, Y: p.Y}
		}
		logger.Infof("setting %d admins (threshold %d) on wallet %s", len(pubKeys), threshold, walletID.Hex())
		if err := fccutils.SetAdmins(s, ownerOpts(s), walletID, pubKeys, threshold); err != nil {
			fccutils.FatalWithCause(err)
		}
		for _, k := range adminKeys {
			if err := fccutils.ConfirmAdmin(s, keyedOpts(s, k), walletID); err != nil {
				fccutils.FatalWithCause(err)
			}
			logger.Infof("admin confirmed: %s", crypto.PubkeyToAddress(k.PublicKey).Hex())
		}
	}

	// c: close initialization
	if strings.Contains(cmd, "c") {
		if err := fccutils.CloseWalletInitialization(s, ownerOpts(s), walletID); err != nil {
			fccutils.FatalWithCause(err)
		}
		logger.Info("wallet initialization closed")
	}

	// t: multisig (cosigner) threshold — 1 for a single-TEE wallet
	if strings.Contains(cmd, "t") {
		if err := fccutils.SetMultisigThreshold(s, ownerOpts(s), walletID, 1); err != nil {
			fccutils.FatalWithCause(err)
		}
		logger.Info("multisig threshold set to 1")
	}

	// k: add key
	var keyID uint64
	if strings.Contains(cmd, "k") {
		opts := ownerOpts(s)
		opts.Value = big.NewInt(1_000_000) // instruction payment (mirror e2e)
		keyID, err = fccutils.AddKey(s, opts, teeID, walletID)
		if err != nil {
			fccutils.FatalWithCause(err)
		}
		logger.Infof("key add requested: walletID %s keyID %d — waiting for TEE key generation", walletID.Hex(), keyID)
		time.Sleep(2 * time.Second)
	}

	// f: fetch proof + confirm key
	if strings.Contains(cmd, "f") {
		proof, sig, err := fccutils.KeyProof(*pf, walletID, keyID)
		if err != nil {
			fccutils.FatalWithCause(err)
		}
		if err := fccutils.ConfirmKey(s, ownerOpts(s), proof, sig); err != nil {
			fccutils.FatalWithCause(err)
		}
		logger.Infof("key confirmed on-chain: keyID %d", keyID)
	}

	// e: enable wallet
	if strings.Contains(cmd, "e") {
		if err := fccutils.EnableWallet(s, ownerOpts(s), walletID); err != nil {
			fccutils.FatalWithCause(err)
		}
		logger.Info("wallet enabled")
	}

	// Derive the HL/EVM address from the on-chain public key and record it.
	keyData, err := fccutils.GetProxyWalletKeyInfo(*pf, walletID, keyID)
	if err != nil {
		fccutils.FatalWithCause(errorf("fetching final key info: %s", err))
	}
	hlAddr, err := keys.PubKeyBytesToAddress(keyData.Info.PublicKey)
	if err != nil {
		fccutils.FatalWithCause(errorf("deriving HL address from pubkey (%d bytes): %s", len(keyData.Info.PublicKey), err))
	}

	reg, err := keys.LoadFileRegistry(*registryF)
	if err != nil {
		fccutils.FatalWithCause(err)
	}
	if err := reg.Put(user, walletID, keyID); err != nil {
		fccutils.FatalWithCause(err)
	}

	logger.Info("=========================================================")
	logger.Infof("  provisioned TEE wallet")
	logger.Infof("  user       : %s", user.Hex())
	logger.Infof("  walletID   : %s", walletID.Hex())
	logger.Infof("  keyID      : %d", keyID)
	logger.Infof("  HL address : %s", hlAddr.Hex())
	logger.Infof("  registry   : %s", *registryF)
	logger.Info("  fund this HL address, then run the extension with HL_KEY_MODE=tee-wallet")
	logger.Info("=========================================================")
}

// ownerOpts builds transact opts for the deployer/owner key.
func ownerOpts(s *support.Support) *bind.TransactOpts {
	return keyedOpts(s, s.Prv)
}

func keyedOpts(s *support.Support, k *ecdsa.PrivateKey) *bind.TransactOpts {
	opts, err := bind.NewKeyedTransactorWithChainID(k, s.ChainID)
	if err != nil {
		fccutils.FatalWithCause(err)
	}
	return opts
}

// fundGasUpTo tops the target up to at least minWei of native gas, sending the
// difference from the deployer. No-op if the target already holds enough.
func fundGasUpTo(s *support.Support, to common.Address, minWei *big.Int) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	bal, err := s.ChainClient.BalanceAt(ctx, to, nil)
	if err != nil {
		return errorf("balance of %s: %s", to.Hex(), err)
	}
	if bal.Cmp(minWei) >= 0 {
		logger.Infof("admin %s already funded (%s wei)", to.Hex(), bal)
		return nil
	}
	topUp := new(big.Int).Sub(minWei, bal)

	nonce, err := s.ChainClient.PendingNonceAt(ctx, crypto.PubkeyToAddress(s.Prv.PublicKey))
	if err != nil {
		return errorf("pending nonce: %s", err)
	}
	gasPrice, err := s.ChainClient.SuggestGasPrice(ctx)
	if err != nil {
		return errorf("suggest gas price: %s", err)
	}
	tx := types.NewTx(&types.LegacyTx{
		Nonce:    nonce,
		To:       &to,
		Value:    topUp,
		Gas:      21000,
		GasPrice: gasPrice,
	})
	signed, err := types.SignTx(tx, types.LatestSignerForChainID(s.ChainID), s.Prv)
	if err != nil {
		return errorf("sign funding tx: %s", err)
	}
	if err := s.ChainClient.SendTransaction(ctx, signed); err != nil {
		return errorf("send funding tx: %s", err)
	}
	if _, err := bind.WaitMined(ctx, s.ChainClient, signed); err != nil {
		return errorf("funding tx not mined: %s", err)
	}
	logger.Infof("funded admin %s with %s wei", to.Hex(), topUp)
	return nil
}

// loadAdminKeys parses HL_WALLET_ADMIN_KEYS (comma-separated hex private keys,
// with or without 0x).
func loadAdminKeys() ([]*ecdsa.PrivateKey, error) {
	raw := os.Getenv("HL_WALLET_ADMIN_KEYS")
	if strings.TrimSpace(raw) == "" {
		return nil, errorf("HL_WALLET_ADMIN_KEYS is not set")
	}
	parts := strings.Split(raw, ",")
	out := make([]*ecdsa.PrivateKey, 0, len(parts))
	for i, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		p = strings.TrimPrefix(strings.TrimPrefix(p, "0x"), "0X")
		k, err := crypto.HexToECDSA(p)
		if err != nil {
			return nil, errorf("admin key %d: %s", i, err)
		}
		out = append(out, k)
	}
	if len(out) == 0 {
		return nil, errorf("HL_WALLET_ADMIN_KEYS parsed to zero keys")
	}
	return out, nil
}

// defaultAdminThreshold returns a majority threshold (2-of-3 for the standard
// 3-admin set): floor(n/2)+1.
func defaultAdminThreshold(n int) uint64 {
	return uint64(n/2 + 1)
}

// parseExtensionID reads the extension ID from the flag or EXTENSION_ID env as a
// hex integer (e.g. 0x...10037).
func parseExtensionID(flagVal string) (*big.Int, error) {
	v := flagVal
	if v == "" {
		v = os.Getenv("EXTENSION_ID")
	}
	if v == "" {
		return nil, errorf("extension ID not set (pass -e or EXTENSION_ID)")
	}
	v = strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(v), "0x"), "0X")
	id, ok := new(big.Int).SetString(v, 16)
	if !ok {
		return nil, errorf("invalid extension ID hex %q", flagVal)
	}
	return id, nil
}

func errorf(format string, a ...any) error { return fmt.Errorf(format, a...) }
