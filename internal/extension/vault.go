package extension

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"time"

	"extension-scaffold/internal/arb"
	"extension-scaffold/internal/config"
	"extension-scaffold/pkg/types"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/flare-foundation/go-flare-common/pkg/logger"
	"github.com/flare-foundation/go-flare-common/pkg/tee/instruction"
	teetypes "github.com/flare-foundation/tee-node/pkg/types"
)

// vault-mode funding: a Coston TestUSDC vault fronts the flow, funds reach HL via
// a central Bridge2 deposit into the treasury (real, or mocked), the treasury
// spotSends to the user, and withdrawals return through the treasury (real
// withdraw3 to the Arbitrum reserve) and a TEE-signed slip the Coston vault
// verifies to release the escrowed TestUSDC. The Coston↔Arbitrum hop is skipped.

// tokenAmountArgs decodes the withdraw/confirmFunding inner payload —
// abi.encode(address token, uint256 amount) built on-chain by the InstructionSender.
var tokenAmountArgs abi.Arguments

// depositPayloadArgs decodes the deposit inner payload —
// abi.encode(address token, uint256 amount, uint256 sponsoredLimit).
var depositPayloadArgs abi.Arguments

func init() {
	addrT, _ := abi.NewType("address", "", nil)
	uintT, _ := abi.NewType("uint256", "", nil)
	tokenAmountArgs = abi.Arguments{{Type: addrT}, {Type: uintT}}
	depositPayloadArgs = abi.Arguments{{Type: addrT}, {Type: uintT}, {Type: uintT}}
}

func decodeTokenAmount(payload []byte) (common.Address, *big.Int, error) {
	vals, err := tokenAmountArgs.Unpack(payload)
	if err != nil {
		return common.Address{}, nil, fmt.Errorf("unpack (token,amount): %w", err)
	}
	if len(vals) != 2 {
		return common.Address{}, nil, fmt.Errorf("expected 2 fields, got %d", len(vals))
	}
	token, ok := vals[0].(common.Address)
	if !ok {
		return common.Address{}, nil, fmt.Errorf("field 0 is not an address")
	}
	amount, ok := vals[1].(*big.Int)
	if !ok || amount == nil {
		return common.Address{}, nil, fmt.Errorf("field 1 is not a uint256")
	}
	return token, amount, nil
}

func decodeDepositPayload(payload []byte) (common.Address, *big.Int, *big.Int, error) {
	vals, err := depositPayloadArgs.Unpack(payload)
	if err != nil {
		return common.Address{}, nil, nil, fmt.Errorf("unpack (token,amount,sponsoredLimit): %w", err)
	}
	if len(vals) != 3 {
		return common.Address{}, nil, nil, fmt.Errorf("expected 3 fields, got %d", len(vals))
	}
	token, ok := vals[0].(common.Address)
	if !ok {
		return common.Address{}, nil, nil, fmt.Errorf("field 0 is not an address")
	}
	amount, ok := vals[1].(*big.Int)
	if !ok || amount == nil {
		return common.Address{}, nil, nil, fmt.Errorf("field 1 is not a uint256")
	}
	limit, ok := vals[2].(*big.Int)
	if !ok || limit == nil {
		return common.Address{}, nil, nil, fmt.Errorf("field 2 is not a uint256")
	}
	return token, amount, limit, nil
}

func (e *Extension) processDepositVault(a teetypes.Action, df *instruction.DataFixed) teetypes.ActionResult {
	user, payload, err := types.DecodeEnvelope(df.OriginalMessage)
	if err != nil {
		return buildResult(a, df, nil, 0, err)
	}
	// The token was already validated against the Vault allow-list on-chain; the
	// amount drives the HL credit and the sponsored limit selects the funding tier.
	_, amountUnits, sponsoredLimit, err := decodeDepositPayload(payload)
	if err != nil {
		return buildResult(a, df, nil, 0, fmt.Errorf("decoding deposit: %w", err))
	}
	usd := arb.UnitsToUSD(amountUnits)
	if usd <= 0 {
		return buildResult(a, df, nil, 0, fmt.Errorf("amount must be positive"))
	}
	if usd < config.MinDepositUSD {
		return buildResult(a, df, nil, 0, fmt.Errorf("deposit %.2f below HL minimum %.2f", usd, config.MinDepositUSD))
	}
	if usd > config.MaxDepositUSD {
		return buildResult(a, df, nil, 0, fmt.Errorf("deposit %.2f exceeds per-deposit cap %.2f", usd, config.MaxDepositUSD))
	}

	mu := e.userMutex(user)
	mu.Lock()
	defer mu.Unlock()

	acct, err := e.accountFor(user)
	if err != nil {
		return buildResult(a, df, nil, 0, fmt.Errorf("deriving account: %w", err))
	}

	// Determine funding tier: sponsored if the user has a non-zero limit AND
	// this deposit keeps their cumulative balance within it.
	limitUsd := arb.UnitsToUSD(sponsoredLimit)
	ledBal := e.led.Balance(user)
	sponsored := limitUsd > 0 && ledBal+usd <= limitUsd
	logger.Infof("deposit tier check: user=%s amount=%.6f limit=%.6f ledgerBal=%.6f sponsored=%v",
		user.Hex(), usd, limitUsd, ledBal, sponsored)

	// Non-sponsored (or over-limit): return the HL address so the user can
	// self-fund via the HL testnet faucet or Arbitrum bridge, then call
	// confirmFunding. The TestUSDC stays escrowed in the Vault; the ledger is
	// credited on confirmFunding.
	if !sponsored {
		resp := types.DepositResponse{
			HLAddress:       acct.Address(),
			SelfFund:        true,
			SelfFundAddress: acct.Address(),
		}
		data, _ := json.Marshal(resp)
		return buildResult(a, df, data, 1, nil)
	}

	// Sponsored: treasury funds the user's HL account (existing flow).
	if e.led.Total()+usd > config.MaxTotalDepositUSD {
		return buildResult(a, df, nil, 0, fmt.Errorf("deposit exceeds global treasury cap"))
	}
	ctx := context.Background()

	// Bring USDC onto HL into the treasury via the real Bridge2 (skipped in mock).
	if !e.bridge2Mock {
		if err := e.fundTreasuryViaBridge(ctx, usd); err != nil {
			return buildResult(a, df, nil, 0, fmt.Errorf("bridge2 funding: %w", err))
		}
	}

	// Treasury spotSends to the user's HL account.
	if err := e.treasury.SendUSD(ctx, acct.Address(), usd); err != nil {
		return buildResult(a, df, nil, 0, fmt.Errorf("treasury spotSend: %w", err))
	}
	e.led.Credit(user, usd)

	resp := types.DepositResponse{HLAddress: acct.Address(), BalanceUsd: e.led.Balance(user)}
	data, _ := json.Marshal(resp)
	return buildResult(a, df, data, 1, nil)
}

// fundTreasuryViaBridge does one real Bridge2 deposit from the Arbitrum reserve,
// which credits the treasury's HL account (reserve == treasury key), waits for the
// credit to land, then sweeps it perp→spot so it can be spotSent. Operator-tuned:
// only runs when HL_BRIDGE2_MOCK=false and the reserve is funded on Arbitrum.
func (e *Extension) fundTreasuryViaBridge(ctx context.Context, usd float64) error {
	units := arb.USDToUnits(usd)
	if err := e.ensureReserveFor(ctx, usd); err != nil {
		return err
	}
	start, _ := e.treasury.PerpWithdrawable(ctx)
	if _, err := e.arb.DepositToBridge(ctx, e.reserveSigner, e.usdcAddr, e.bridgeAddr, units); err != nil {
		return fmt.Errorf("deposit to bridge2: %w", err)
	}
	// The credit lands in the treasury's perp/Core balance after ~1 min.
	tctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		if w, err := e.treasury.PerpWithdrawable(ctx); err == nil && w >= start+usd*0.9 {
			break
		}
		select {
		case <-tctx.Done():
			return fmt.Errorf("timed out waiting for treasury HL credit")
		case <-ticker.C:
		}
	}
	// Sweep the credit perp→spot so the treasury can spotSend it.
	if free, err := e.treasury.PerpWithdrawable(ctx); err == nil && free > 0 {
		if err := e.treasury.ClassTransfer(ctx, free, false); err != nil {
			return fmt.Errorf("treasury perp->spot: %w", err)
		}
	}
	return nil
}

// processConfirmFunding handles CONFIRM_FUNDING instructions from self-fund
// users. The user previously deposited TestUSDC (escrowed in the Vault), received
// their HL address, funded it via the HL testnet faucet or Arbitrum bridge, and
// now confirms. The TEE verifies the HL spot balance covers the claimed amount
// and credits the ledger.
func (e *Extension) processConfirmFunding(a teetypes.Action, df *instruction.DataFixed) teetypes.ActionResult {
	user, payload, err := types.DecodeEnvelope(df.OriginalMessage)
	if err != nil {
		return buildResult(a, df, nil, 0, err)
	}
	_, amountUnits, err := decodeTokenAmount(payload)
	if err != nil {
		return buildResult(a, df, nil, 0, fmt.Errorf("decoding confirmFunding: %w", err))
	}
	usd := arb.UnitsToUSD(amountUnits)
	if usd <= 0 {
		return buildResult(a, df, nil, 0, fmt.Errorf("amount must be positive"))
	}

	mu := e.userMutex(user)
	mu.Lock()
	defer mu.Unlock()

	acct, err := e.accountFor(user)
	if err != nil {
		return buildResult(a, df, nil, 0, fmt.Errorf("deriving account: %w", err))
	}

	// Verify the user actually funded their HL address.
	ctx := context.Background()
	spotBal, err := acct.SpotUSDC(ctx)
	if err != nil {
		return buildResult(a, df, nil, 0, fmt.Errorf("reading HL spot balance: %w", err))
	}
	if spotBal < usd {
		return buildResult(a, df, nil, 0, fmt.Errorf("HL spot balance %.2f is below claimed %.2f — fund your HL address first", spotBal, usd))
	}

	e.led.Credit(user, usd)

	resp := types.ConfirmFundingResponse{HLAddress: acct.Address(), BalanceUsd: e.led.Balance(user)}
	data, _ := json.Marshal(resp)
	return buildResult(a, df, data, 1, nil)
}

func (e *Extension) processWithdrawVault(a teetypes.Action, df *instruction.DataFixed) teetypes.ActionResult {
	user, payload, err := types.DecodeEnvelope(df.OriginalMessage)
	if err != nil {
		return buildResult(a, df, nil, 0, err)
	}
	token, amountUnits, err := decodeTokenAmount(payload)
	if err != nil {
		return buildResult(a, df, nil, 0, fmt.Errorf("decoding withdraw: %w", err))
	}
	usd := arb.UnitsToUSD(amountUnits)
	if usd <= 0 {
		return buildResult(a, df, nil, 0, fmt.Errorf("amount must be positive"))
	}

	mu := e.userMutex(user)
	mu.Lock()
	defer mu.Unlock()

	if err := e.led.Debit(user, usd); err != nil {
		return buildResult(a, df, nil, 0, err)
	}
	acct, err := e.accountFor(user)
	if err != nil {
		e.led.Credit(user, usd) // refund the debit
		return buildResult(a, df, nil, 0, err)
	}
	ctx := context.Background()

	// 1. Reclaim the user's HL funds to the treasury. Perp trades leave collateral
	//    on the perp side, so sweep free perp margin to spot first, then spotSend.
	if free, err := acct.PerpWithdrawable(ctx); err == nil && free > 1.0 {
		_ = acct.ClassTransfer(ctx, free, false) // perp -> spot, best-effort
	}
	if err := acct.SendUSD(ctx, e.treasuryAddr, usd); err != nil {
		e.led.Credit(user, usd) // refund; nothing has moved on Coston yet
		return buildResult(a, df, nil, 0, fmt.Errorf("reclaiming to treasury (close positions first?): %w", err))
	}

	// 2. Real path: withdraw3 the reclaimed funds from the treasury to the Arbitrum
	//    reserve. Funds stay on Arbitrum (the Coston↔Arbitrum hop is skipped).
	//    Best-effort — the funds are safe in the treasury HL account regardless.
	if !e.bridge2Mock {
		if err := e.treasury.Withdraw(ctx, e.reserveAddr.Hex(), usd); err != nil {
			logger.Warnf("withdraw3 treasury->reserve: %v (funds remain in treasury HL)", err)
		}
	}

	// 3. Sign the Vault slip so the user can release their escrowed TestUSDC on
	//    Coston. The withdrawalId is the on-chain instructionId (unique, single-use).
	withdrawalId := df.InstructionID
	sig, err := e.signWithdrawalSlip(user, token, amountUnits, withdrawalId)
	if err != nil {
		// The HL side is already reclaimed and the ledger debit is correct; do not
		// refund. Signing is local crypto and only fails on a misconfigured key.
		logger.Errorf("signing withdrawal slip for %s: %v", user.Hex(), err)
		return buildResult(a, df, nil, 0, fmt.Errorf("signing withdrawal slip: %w", err))
	}

	resp := types.WithdrawResponse{
		BalanceUsd:   e.led.Balance(user),
		Vault:        e.vaultAddr.Hex(),
		User:         user.Hex(),
		Token:        token.Hex(),
		Amount:       amountUnits.String(),
		WithdrawalId: withdrawalId.Hex(),
		Signature:    hexutil.Encode(sig),
	}
	data, _ := json.Marshal(resp)
	return buildResult(a, df, data, 1, nil)
}

// signWithdrawalSlip produces the EIP-191 signature the Vault verifies in
// executeWithdraw: personal_sign over keccak256(abi.encodePacked(vault, user,
// token, amount, withdrawalId)).
func (e *Extension) signWithdrawalSlip(user, token common.Address, amount *big.Int, withdrawalId common.Hash) ([]byte, error) {
	if e.vaultAuthKey == nil {
		return nil, fmt.Errorf("vault authority key not configured")
	}
	packed := make([]byte, 0, 20+20+20+32+32)
	packed = append(packed, e.vaultAddr.Bytes()...)
	packed = append(packed, user.Bytes()...)
	packed = append(packed, token.Bytes()...)
	packed = append(packed, common.LeftPadBytes(amount.Bytes(), 32)...)
	packed = append(packed, withdrawalId.Bytes()...)

	ethHash := accounts.TextHash(crypto.Keccak256(packed))
	sig, err := crypto.Sign(ethHash, e.vaultAuthKey)
	if err != nil {
		return nil, err
	}
	sig[64] += 27 // v: {0,1} -> {27,28} for Solidity ecrecover
	return sig, nil
}
