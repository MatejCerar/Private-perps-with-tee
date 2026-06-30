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

func sendHL(
	s *support.Support,
	instructionSenderAddress common.Address,
	call func(opts *bind.TransactOpts, payload []byte) (*types.Transaction, error),
	payload []byte,
) (common.Hash, common.Hash, error) {
	opts, err := bind.NewKeyedTransactorWithChainID(s.Prv, s.ChainID)
	if err != nil {
		return common.Hash{}, common.Hash{}, errors.Errorf("failed to create transactor: %s", err)
	}
	opts.Value = big.NewInt(1000000) // instruction fee in wei — must match registry's required fee

	tx, err := call(opts, payload)
	if err != nil {
		return common.Hash{}, common.Hash{}, errors.Errorf("failed to send instruction: %s", err)
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

func SendDeposit(s *support.Support, addr common.Address, payload []byte) (common.Hash, common.Hash, error) {
	c, err := bindSender(s, addr)
	if err != nil {
		return common.Hash{}, common.Hash{}, err
	}
	return sendHL(s, addr, c.Deposit, payload)
}

func SendPlaceOrder(s *support.Support, addr common.Address, payload []byte) (common.Hash, common.Hash, error) {
	c, err := bindSender(s, addr)
	if err != nil {
		return common.Hash{}, common.Hash{}, err
	}
	return sendHL(s, addr, c.PlaceOrder, payload)
}

func SendCancel(s *support.Support, addr common.Address, payload []byte) (common.Hash, common.Hash, error) {
	c, err := bindSender(s, addr)
	if err != nil {
		return common.Hash{}, common.Hash{}, err
	}
	return sendHL(s, addr, c.Cancel, payload)
}

func SendWithdraw(s *support.Support, addr common.Address, payload []byte) (common.Hash, common.Hash, error) {
	c, err := bindSender(s, addr)
	if err != nil {
		return common.Hash{}, common.Hash{}, err
	}
	return sendHL(s, addr, c.Withdraw, payload)
}
