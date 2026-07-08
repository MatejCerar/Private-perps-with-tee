package fccutils

import (
	"encoding/hex"
	"encoding/json"
	"extension-scaffold/tools/pkg/support"
	"math/big"
	"net/http"
	"strconv"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/flare-foundation/go-flare-common/pkg/contracts/tee/walletkeymanager"
	"github.com/flare-foundation/go-flare-common/pkg/contracts/tee/walletmanager"
	"github.com/pkg/errors"
	pwallets "github.com/flare-foundation/tee-proxy/pkg/wallets"
)

// This file ports the wallet-lifecycle contract calls and proxy key-info fetch
// from tee/e2e (pkg/utils/wallet_txs.go + tee_calls.go) onto this repo's
// *support.Support (diamond-bound WalletProjectManager / WalletManager /
// WalletKeyManager). tools/cmd/provision-wallet orchestrates them to create a
// TEE-custodied EVM key for a user (C9 / PMW custody).

// keyProofPollAttempts bounds how long KeyProof waits for the proxy to publish a
// freshly-generated key's existence proof (attempts × 2s).
const keyProofPollAttempts = 30

// CreateProject creates a wallet project for the extension bound to a key type
// and signing algorithm, returning the on-chain project ID.
func CreateProject(s *support.Support, opts *bind.TransactOpts, extensionID *big.Int, keyType, signingAlgo common.Hash) (common.Hash, error) {
	tx, err := s.TeeWalletProjectManager.CreateProject(opts, extensionID, keyType, signingAlgo)
	if err != nil {
		return common.Hash{}, errors.Errorf("createProject: %s", err)
	}
	receipt, err := support.CheckTx(tx, s.ChainClient)
	if err != nil {
		return common.Hash{}, err
	}
	if len(receipt.Logs) == 0 {
		return common.Hash{}, errors.New("createProject: no logs in receipt")
	}
	ev, err := s.TeeWalletProjectManager.ParseProjectCreated(*receipt.Logs[0])
	if err != nil {
		return common.Hash{}, errors.Errorf("parse ProjectCreated: %s", err)
	}
	return ev.ProjectId, nil
}

// CreateWallet creates a wallet in the given project, returning its wallet ID.
func CreateWallet(s *support.Support, opts *bind.TransactOpts, projectID common.Hash) (common.Hash, error) {
	tx, err := s.TeeWalletManager.CreateWallet(opts, projectID)
	if err != nil {
		return common.Hash{}, errors.Errorf("createWallet: %s", err)
	}
	receipt, err := support.CheckTx(tx, s.ChainClient)
	if err != nil {
		return common.Hash{}, err
	}
	if len(receipt.Logs) == 0 {
		return common.Hash{}, errors.New("createWallet: no logs in receipt")
	}
	ev, err := s.TeeWalletManager.ParseWalletCreated(*receipt.Logs[0])
	if err != nil {
		return common.Hash{}, errors.Errorf("parse WalletCreated: %s", err)
	}
	return ev.WalletId, nil
}

// SetAdmins sets the admin public keys and the admin (restore) threshold for a
// wallet. Each admin must subsequently ConfirmAdmin from its own key.
func SetAdmins(s *support.Support, opts *bind.TransactOpts, walletID common.Hash, adminKeys []walletmanager.PublicKey, threshold uint64) error {
	tx, err := s.TeeWalletManager.SetAdmins(opts, walletID, adminKeys, threshold)
	if err != nil {
		return errors.Errorf("setAdmins: %s", err)
	}
	if _, err := support.CheckTx(tx, s.ChainClient); err != nil {
		return err
	}
	return nil
}

// ConfirmAdmin confirms the caller (opts.From) as an admin of the wallet.
func ConfirmAdmin(s *support.Support, opts *bind.TransactOpts, walletID common.Hash) error {
	tx, err := s.TeeWalletManager.ConfirmAdmin(opts, walletID)
	if err != nil {
		return errors.Errorf("confirmAdmin: %s", err)
	}
	if _, err := support.CheckTx(tx, s.ChainClient); err != nil {
		return err
	}
	return nil
}

// CloseWalletInitialization ends the wallet's initialization phase, freezing its
// admin/config constants so keys can be added.
func CloseWalletInitialization(s *support.Support, opts *bind.TransactOpts, walletID common.Hash) error {
	tx, err := s.TeeWalletManager.CloseWalletInitialization(opts, walletID)
	if err != nil {
		return errors.Errorf("closeWalletInitialization: %s", err)
	}
	if _, err := support.CheckTx(tx, s.ChainClient); err != nil {
		return err
	}
	return nil
}

// SetMultisigThreshold sets the cosigner signing threshold on the wallet key
// manager. For a single-TEE wallet this is 1 (the TEE signs alone).
func SetMultisigThreshold(s *support.Support, opts *bind.TransactOpts, walletID common.Hash, threshold uint64) error {
	tx, err := s.TeeWalletKeyManager.SetMultisigThreshold(opts, walletID, threshold)
	if err != nil {
		return errors.Errorf("setMultisigThreshold: %s", err)
	}
	if _, err := support.CheckTx(tx, s.ChainClient); err != nil {
		return err
	}
	return nil
}

// AddKey requests the TEE (teeID) to generate a key for the wallet, returning
// the assigned key ID. opts.Value must be non-zero (the instruction payment).
func AddKey(s *support.Support, opts *bind.TransactOpts, teeID common.Address, walletID common.Hash) (uint64, error) {
	tx, err := s.TeeWalletKeyManager.AddKey(opts, teeID, walletID, opts.From)
	if err != nil {
		return 0, errors.Errorf("addKey: %s", err)
	}
	receipt, err := support.CheckTx(tx, s.ChainClient)
	if err != nil {
		return 0, err
	}
	if len(receipt.Logs) == 0 {
		return 0, errors.New("addKey: no logs in receipt")
	}
	ev, err := s.TeeWalletKeyManager.ParseWalletKeyAdded(*receipt.Logs[0])
	if err != nil {
		return 0, errors.Errorf("parse WalletKeyAdded: %s", err)
	}
	return ev.KeyId, nil
}

// ConfirmKey confirms an added key on-chain using the TEE's existence proof and
// signature (fetched from the proxy via KeyProof).
func ConfirmKey(s *support.Support, opts *bind.TransactOpts, proof *walletkeymanager.IWalletKeyManagerKeyExistence, signature *walletkeymanager.Signature) error {
	tx, err := s.TeeWalletKeyManager.ConfirmKey(opts, *proof, *signature)
	if err != nil {
		return errors.Errorf("confirmKey: %s", err)
	}
	if _, err := support.CheckTx(tx, s.ChainClient); err != nil {
		return err
	}
	return nil
}

// EnableWallet transitions the wallet to enabled so its keys can be used.
func EnableWallet(s *support.Support, opts *bind.TransactOpts, walletID common.Hash) error {
	tx, err := s.TeeWalletManager.EnableWallet(opts, walletID)
	if err != nil {
		return errors.Errorf("enableWallet: %s", err)
	}
	receipt, err := support.CheckTx(tx, s.ChainClient)
	if err != nil {
		return err
	}
	if len(receipt.Logs) == 0 {
		return errors.New("enableWallet: no logs in receipt")
	}
	ev, err := s.TeeWalletManager.ParseWalletEnabled(*receipt.Logs[0])
	if err != nil {
		return errors.Errorf("parse WalletEnabled: %s", err)
	}
	if ev.WalletId != walletID {
		return errors.New("enableWallet: enabled wallet ID mismatch")
	}
	return nil
}

// GetProxyWalletKeyInfo fetches a key's existence record + proof from the public
// tee-proxy, polling until the proxy has it (a freshly added key takes a few
// seconds to be generated and published).
func GetProxyWalletKeyInfo(proxyURL string, walletID common.Hash, keyID uint64) (*pwallets.KeyData, error) {
	url := proxyURL + "/wallet/" + hex.EncodeToString(walletID[:]) + "/" + strconv.FormatUint(keyID, 10)
	var (
		resp *http.Response
		err  error
	)
	for range keyProofPollAttempts {
		resp, err = http.Get(url)
		if err == nil && resp.StatusCode == http.StatusOK {
			break
		}
		if resp != nil {
			resp.Body.Close()
		}
		time.Sleep(2 * time.Second)
	}
	if err != nil {
		return nil, errors.Errorf("get wallet key info: %s", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, errors.Errorf("get wallet key info: status %d", resp.StatusCode)
	}
	defer resp.Body.Close()

	var keyData pwallets.KeyData
	if err := json.NewDecoder(resp.Body).Decode(&keyData); err != nil {
		return nil, errors.Errorf("decode wallet key info: %s", err)
	}
	return &keyData, nil
}

// KeyProof fetches a key's existence proof from the proxy and shapes it into the
// (proof, signature) pair ConfirmKey expects.
func KeyProof(proxyURL string, walletID common.Hash, keyID uint64) (*walletkeymanager.IWalletKeyManagerKeyExistence, *walletkeymanager.Signature, error) {
	keyData, err := GetProxyWalletKeyInfo(proxyURL, walletID, keyID)
	if err != nil {
		return nil, nil, err
	}
	if keyData.Proof == nil || len(keyData.Proof.Signature) != 65 {
		return nil, nil, errors.New("key proof missing or malformed signature")
	}

	adminPubKeys := make([]walletkeymanager.PublicKey, len(keyData.Info.ConfigConstants.AdminsPublicKeys))
	for i, e := range keyData.Info.ConfigConstants.AdminsPublicKeys {
		adminPubKeys[i] = walletkeymanager.PublicKey{X: e.X, Y: e.Y}
	}

	proof := &walletkeymanager.IWalletKeyManagerKeyExistence{
		TeeId:       keyData.Info.TeeID,
		WalletId:    keyData.Info.WalletID,
		KeyId:       keyData.Info.KeyID,
		KeyType:     keyData.Info.KeyType,
		SigningAlgo: keyData.Info.SigningAlgo,
		PublicKey:   keyData.Info.PublicKey,
		Nonce:       keyData.Info.Nonce,
		Restored:    keyData.Info.Restored,
		ConfigConstants: walletkeymanager.IWalletKeyManagerKeyConfigConstants{
			AdminsPublicKeys:   adminPubKeys,
			AdminsThreshold:    keyData.Info.ConfigConstants.AdminsThreshold,
			Cosigners:          keyData.Info.ConfigConstants.Cosigners,
			CosignersThreshold: keyData.Info.ConfigConstants.CosignersThreshold,
		},
		SettingsVersion: keyData.Info.SettingsVersion,
		Settings:        keyData.Info.Settings,
	}

	signature := &walletkeymanager.Signature{
		R: [32]byte(keyData.Proof.Signature[0:32]),
		S: [32]byte(keyData.Proof.Signature[32:64]),
		V: keyData.Proof.Signature[64] + 27,
	}

	return proof, signature, nil
}
