package utils

import (
	"context"

	"extension-scaffold/tools/pkg/contracts/testusdc"
	"extension-scaffold/tools/pkg/contracts/vault"
	"extension-scaffold/tools/pkg/support"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/pkg/errors"
)

func deployWait(s *support.Support, tx *types.Transaction, err error, what string) error {
	if err != nil {
		return errors.Errorf("%s: %s", what, err)
	}
	receipt, err := bind.WaitMined(context.Background(), s.ChainClient, tx)
	if err != nil {
		return errors.Errorf("%s wait: %s", what, err)
	}
	if receipt.Status != types.ReceiptStatusSuccessful {
		return errors.Errorf("%s failed", what)
	}
	return nil
}

// DeployTestUSDC deploys the mintable TestUSDC token on Coston.
func DeployTestUSDC(s *support.Support) (common.Address, error) {
	opts, err := bind.NewKeyedTransactorWithChainID(s.Prv, s.ChainID)
	if err != nil {
		return common.Address{}, errors.Errorf("transactor: %s", err)
	}
	addr, tx, _, err := testusdc.DeployTestUSDC(opts, s.ChainClient)
	if err := deployWait(s, tx, err, "deploy TestUSDC"); err != nil {
		return common.Address{}, err
	}
	return addr, nil
}

// DeployVault deploys the Vault owned by `owner`.
func DeployVault(s *support.Support, owner common.Address) (common.Address, error) {
	opts, err := bind.NewKeyedTransactorWithChainID(s.Prv, s.ChainID)
	if err != nil {
		return common.Address{}, errors.Errorf("transactor: %s", err)
	}
	opts.GasLimit = 1_500_000 // Coston2 gas estimation underestimates code storage cost
	addr, tx, _, err := vault.DeployVault(opts, s.ChainClient, owner)
	if err := deployWait(s, tx, err, "deploy Vault"); err != nil {
		return common.Address{}, err
	}
	return addr, nil
}

// VaultSetAllowedToken allow-lists a token on the Vault (owner only).
func VaultSetAllowedToken(s *support.Support, vaultAddr, token common.Address, allowed bool) error {
	v, err := vault.NewVault(vaultAddr, s.ChainClient)
	if err != nil {
		return errors.Errorf("bind Vault: %s", err)
	}
	opts, err := bind.NewKeyedTransactorWithChainID(s.Prv, s.ChainID)
	if err != nil {
		return errors.Errorf("transactor: %s", err)
	}
	tx, err := v.SetAllowedToken(opts, token, allowed)
	return deployWait(s, tx, err, "vault.setAllowedToken")
}

// VaultSetTeeAuthority records the TEE's signing address on the Vault (one-shot).
// Called post-TEE-registration once the TEE authority key's address is known.
func VaultSetTeeAuthority(s *support.Support, vaultAddr, tee common.Address) error {
	v, err := vault.NewVault(vaultAddr, s.ChainClient)
	if err != nil {
		return errors.Errorf("bind Vault: %s", err)
	}
	opts, err := bind.NewKeyedTransactorWithChainID(s.Prv, s.ChainID)
	if err != nil {
		return errors.Errorf("transactor: %s", err)
	}
	tx, err := v.SetTeeAuthority(opts, tee)
	return deployWait(s, tx, err, "vault.setTeeAuthority")
}
