package utils

import (
	"context"
	"math/big"
	"time"

	"extension-scaffold/tools/pkg/contracts/hyperliquid"
	"extension-scaffold/tools/pkg/fccutils"
	"extension-scaffold/tools/pkg/support"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/pkg/errors"
)

func DeployInstructionSender(s *support.Support) (common.Address, *hyperliquid.HyperliquidInstructionSender, error) {
	opts, err := bind.NewKeyedTransactorWithChainID(s.Prv, s.ChainID)
	if err != nil {
		return common.Address{}, nil, errors.Errorf("failed to create transactor: %s", err)
	}

	opts.GasLimit = 3_000_000 // Coston2 gas estimation underestimates code storage cost

	// Both registry args are the FlareTeeManager diamond proxy: the diamond
	// routes ExtensionManager and MachineManager calls to the right facets.
	address, tx, contract, err := hyperliquid.DeployHyperliquidInstructionSender(
		opts, s.ChainClient, s.Addresses.FlareTeeManager, s.Addresses.FlareTeeManager,
	)
	if err != nil {
		return common.Address{}, nil, errors.Errorf("failed to deploy contract: %s", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	receipt, err := bind.WaitMined(ctx, s.ChainClient, tx)
	if err != nil {
		return common.Address{}, nil, errors.Errorf("deployment tx not mined within 2 minutes (tx: %s): %s", tx.Hash().Hex(), err)
	}

	if receipt.Status != types.ReceiptStatusSuccessful {
		return common.Address{}, nil, errors.New("contract deployment failed")
	}

	return address, contract, nil
}

func SetExtensionId(s *support.Support, instructionSenderAddress common.Address) error {
	sender, err := hyperliquid.NewHyperliquidInstructionSender(instructionSenderAddress, s.ChainClient)
	if err != nil {
		return errors.Errorf("failed to bind contract: %s", err)
	}

	opts, err := bind.NewKeyedTransactorWithChainID(s.Prv, s.ChainID)
	if err != nil {
		return errors.Errorf("failed to create transactor: %s", err)
	}

	tx, err := sender.SetExtensionId(opts)
	if err != nil {
		reason := fccutils.DecodeRevertReason(err)
		if reason == "" {
			parsed, _ := hyperliquid.HyperliquidInstructionSenderMetaData.GetAbi()
			if parsed != nil {
				callData, packErr := parsed.Pack("setExtensionId")
				if packErr == nil {
					from := crypto.PubkeyToAddress(s.Prv.PublicKey)
					reason = fccutils.SimulateAndDecodeRevert(
						s.ChainClient, from, instructionSenderAddress, nil, callData,
					)
				}
			}
		}
		if reason != "" {
			return errors.Errorf("failed to call setExtensionId: %s (revert reason: %s)", err, reason)
		}
		return errors.Errorf("failed to call setExtensionId: %s", err)
	}

	receipt, err := bind.WaitMined(context.Background(), s.ChainClient, tx)
	if err != nil {
		return errors.Errorf("failed waiting for transaction: %s", err)
	}

	if receipt.Status != types.ReceiptStatusSuccessful {
		parsed, _ := hyperliquid.HyperliquidInstructionSenderMetaData.GetAbi()
		if parsed != nil {
			callData, packErr := parsed.Pack("setExtensionId")
			if packErr == nil {
				from := crypto.PubkeyToAddress(s.Prv.PublicKey)
				reason := fccutils.SimulateAndDecodeRevert(
					s.ChainClient, from, instructionSenderAddress, nil, callData,
				)
				if reason != "" {
					return errors.Errorf("setExtensionId transaction failed (revert reason: %s)", reason)
				}
			}
		}
		return errors.New("setExtensionId transaction failed")
	}

	return nil
}

// feeOpts builds a transactor that forwards the registry instruction fee.
func feeOpts(s *support.Support) (*bind.TransactOpts, error) {
	opts, err := bind.NewKeyedTransactorWithChainID(s.Prv, s.ChainID)
	if err != nil {
		return nil, errors.Errorf("failed to create transactor: %s", err)
	}
	opts.Value = big.NewInt(1000000) // instruction fee in wei — must match registry's required fee
	return opts, nil
}

// finalize waits for the instruction tx, checks status, and extracts the
// instructionId from the TeeInstructionsSent event.
func finalize(s *support.Support, tx *types.Transaction, sendErr error) (common.Hash, common.Hash, error) {
	if sendErr != nil {
		return common.Hash{}, common.Hash{}, errors.Errorf("failed to send instruction: %s", sendErr)
	}
	receipt, err := bind.WaitMined(context.Background(), s.ChainClient, tx)
	if err != nil {
		return common.Hash{}, common.Hash{}, errors.Errorf("failed waiting for transaction: %s", err)
	}
	if receipt.Status != 1 {
		return common.Hash{}, common.Hash{}, errors.Errorf("transaction failed with status: %d", receipt.Status)
	}
	if len(receipt.Logs) == 0 {
		return common.Hash{}, common.Hash{}, errors.New("no logs found in receipt")
	}
	sent, err := s.TeeVerification.ParseTeeInstructionsSent(*receipt.Logs[0])
	if err != nil {
		return common.Hash{}, common.Hash{}, errors.Errorf("failed to parse TeeInstructionsSent event: %s", err)
	}
	return sent.InstructionId, receipt.TxHash, nil
}

func bindSender(s *support.Support, addr common.Address) (*hyperliquid.HyperliquidInstructionSender, error) {
	return hyperliquid.NewHyperliquidInstructionSender(addr, s.ChainClient)
}

// SetVault wires the Coston Vault into the InstructionSender (one-shot, post-deploy).
func SetVault(s *support.Support, addr, vault common.Address) error {
	c, err := bindSender(s, addr)
	if err != nil {
		return err
	}
	opts, err := bind.NewKeyedTransactorWithChainID(s.Prv, s.ChainID)
	if err != nil {
		return errors.Errorf("failed to create transactor: %s", err)
	}
	tx, err := c.SetVault(opts, vault)
	if err != nil {
		return errors.Errorf("failed to call setVault: %s", err)
	}
	receipt, err := bind.WaitMined(context.Background(), s.ChainClient, tx)
	if err != nil {
		return errors.Errorf("failed waiting for setVault: %s", err)
	}
	if receipt.Status != 1 {
		return errors.New("setVault transaction failed")
	}
	return nil
}

func SendDeposit(s *support.Support, addr, token common.Address, amount *big.Int) (common.Hash, common.Hash, error) {
	c, err := bindSender(s, addr)
	if err != nil {
		return common.Hash{}, common.Hash{}, err
	}
	opts, err := feeOpts(s)
	if err != nil {
		return common.Hash{}, common.Hash{}, err
	}
	tx, err := c.Deposit(opts, token, amount)
	return finalize(s, tx, err)
}

func SendRequestWithdraw(s *support.Support, addr, token common.Address, amount *big.Int) (common.Hash, common.Hash, error) {
	c, err := bindSender(s, addr)
	if err != nil {
		return common.Hash{}, common.Hash{}, err
	}
	opts, err := feeOpts(s)
	if err != nil {
		return common.Hash{}, common.Hash{}, err
	}
	tx, err := c.RequestWithdraw(opts, token, amount)
	return finalize(s, tx, err)
}

func SendPlaceOrder(s *support.Support, addr common.Address, payload []byte) (common.Hash, common.Hash, error) {
	c, err := bindSender(s, addr)
	if err != nil {
		return common.Hash{}, common.Hash{}, err
	}
	opts, err := feeOpts(s)
	if err != nil {
		return common.Hash{}, common.Hash{}, err
	}
	tx, err := c.PlaceOrder(opts, payload)
	return finalize(s, tx, err)
}

func SendCancel(s *support.Support, addr common.Address, payload []byte) (common.Hash, common.Hash, error) {
	c, err := bindSender(s, addr)
	if err != nil {
		return common.Hash{}, common.Hash{}, err
	}
	opts, err := feeOpts(s)
	if err != nil {
		return common.Hash{}, common.Hash{}, err
	}
	tx, err := c.Cancel(opts, payload)
	return finalize(s, tx, err)
}

// SetSponsoredLimit sets a user's sponsored funding limit on the InstructionSender
// (owner-only). The limit is in token units (6 decimals for TestUSDC). Zero
// revokes sponsorship; non-zero lets the treasury fund deposits up to this
// cumulative limit. Example: 5000e6 = $5,000.
func SetSponsoredLimit(s *support.Support, addr, user common.Address, limit *big.Int) error {
	c, err := bindSender(s, addr)
	if err != nil {
		return err
	}
	opts, err := bind.NewKeyedTransactorWithChainID(s.Prv, s.ChainID)
	if err != nil {
		return errors.Errorf("failed to create transactor: %s", err)
	}
	tx, err := c.SetSponsoredLimit(opts, user, limit)
	if err != nil {
		return errors.Errorf("failed to call setSponsoredLimit: %s", err)
	}
	receipt, err := bind.WaitMined(context.Background(), s.ChainClient, tx)
	if err != nil {
		return errors.Errorf("failed waiting for setSponsoredLimit: %s", err)
	}
	if receipt.Status != 1 {
		return errors.New("setSponsoredLimit transaction failed")
	}
	return nil
}

// SendConfirmFunding sends a CONFIRM_FUNDING instruction after a self-fund user
// has funded their HL address. The TEE verifies the HL balance and credits the ledger.
func SendConfirmFunding(s *support.Support, addr, token common.Address, amount *big.Int) (common.Hash, common.Hash, error) {
	c, err := bindSender(s, addr)
	if err != nil {
		return common.Hash{}, common.Hash{}, err
	}
	opts, err := feeOpts(s)
	if err != nil {
		return common.Hash{}, common.Hash{}, err
	}
	tx, err := c.ConfirmFunding(opts, token, amount)
	return finalize(s, tx, err)
}

// SetAdmin grants or revokes admin status on the InstructionSender (owner-only).
// Admins can call setSponsoredLimit to manage the funding allowlist.
func SetAdmin(s *support.Support, addr, admin common.Address, status bool) error {
	c, err := bindSender(s, addr)
	if err != nil {
		return err
	}
	opts, err := bind.NewKeyedTransactorWithChainID(s.Prv, s.ChainID)
	if err != nil {
		return errors.Errorf("failed to create transactor: %s", err)
	}
	tx, err := c.SetAdmin(opts, admin, status)
	if err != nil {
		return errors.Errorf("failed to call setAdmin: %s", err)
	}
	receipt, err := bind.WaitMined(context.Background(), s.ChainClient, tx)
	if err != nil {
		return errors.Errorf("failed waiting for setAdmin: %s", err)
	}
	if receipt.Status != 1 {
		return errors.New("setAdmin transaction failed")
	}
	return nil
}
