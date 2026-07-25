package main

import (
	"context"
	"math"
	"math/big"

	"extension-scaffold/tools/pkg/contracts/testusdc"
	"extension-scaffold/tools/pkg/contracts/vault"
	"extension-scaffold/tools/pkg/support"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/pkg/errors"
)

// usdToUnits converts USD to 6-decimal token units (TestUSDC has 6 decimals).
func usdToUnits(usd float64) *big.Int {
	return big.NewInt(int64(math.Round(usd * 1e6)))
}

// sendAndWait builds a fresh transactor (auto-nonce), sends, and waits for success.
func sendAndWait(s *support.Support, send func(*bind.TransactOpts) (*types.Transaction, error), what string) error {
	opts, err := bind.NewKeyedTransactorWithChainID(s.Prv, s.ChainID)
	if err != nil {
		return errors.Errorf("%s transactor: %s", what, err)
	}
	tx, err := send(opts)
	if err != nil {
		return errors.Errorf("%s: %s", what, err)
	}
	receipt, err := bind.WaitMined(context.Background(), s.ChainClient, tx)
	if err != nil {
		return errors.Errorf("%s wait: %s", what, err)
	}
	if receipt.Status != 1 {
		return errors.Errorf("%s tx failed (status %d)", what, receipt.Status)
	}
	return nil
}

// mintAndApprove faucets TestUSDC to the caller and approves the InstructionSender
// to escrow it into the vault on deposit.
func mintAndApprove(s *support.Support, token, spender common.Address, amount *big.Int) error {
	t, err := testusdc.NewTestUSDC(token, s.ChainClient)
	if err != nil {
		return errors.Errorf("bind TestUSDC: %s", err)
	}
	me := crypto.PubkeyToAddress(s.Prv.PublicKey)
	if err := sendAndWait(s, func(o *bind.TransactOpts) (*types.Transaction, error) {
		return t.Mint(o, me, amount)
	}, "mint"); err != nil {
		return err
	}
	return sendAndWait(s, func(o *bind.TransactOpts) (*types.Transaction, error) {
		return t.Approve(o, spender, amount)
	}, "approve")
}

// executeVaultWithdraw submits the TEE-signed slip to the vault to release funds.
func executeVaultWithdraw(s *support.Support, vaultAddr common.Address, wd withdrawResult) error {
	v, err := vault.NewVault(vaultAddr, s.ChainClient)
	if err != nil {
		return errors.Errorf("bind Vault: %s", err)
	}
	amount, ok := new(big.Int).SetString(wd.Amount, 10)
	if !ok {
		return errors.Errorf("bad amount %q", wd.Amount)
	}
	var wid [32]byte
	copy(wid[:], common.HexToHash(wd.WithdrawalId).Bytes())
	user := common.HexToAddress(wd.User)
	token := common.HexToAddress(wd.Token)
	sig := common.FromHex(wd.Signature)
	return sendAndWait(s, func(o *bind.TransactOpts) (*types.Transaction, error) {
		return v.ExecuteWithdraw(o, user, token, amount, wid, sig)
	}, "executeWithdraw")
}
