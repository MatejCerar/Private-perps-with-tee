package extension

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"time"

	"extension-scaffold/internal/arb"
	"extension-scaffold/internal/config"
	"extension-scaffold/internal/hl"
	"extension-scaffold/internal/keys"
	"extension-scaffold/pkg/types"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/flare-foundation/go-flare-common/pkg/tee/instruction"
	teetypes "github.com/flare-foundation/tee-node/pkg/types"
)

// Bridge-funding tunables.
const (
	depositCreditTolerance = 1.0             // USDC; a deposit may credit slightly less
	hlCreditTimeout        = 4 * time.Minute // HL finalizes a deposit in ~1 min
	reserveFundTimeout     = 2 * time.Minute
)

// Gas dust sent to a user's Arbitrum address so it can pay for its own Bridge2
// deposit tx (only when its balance is below minUserGasWei). go-ethereum copies
// these into each tx, so sharing the package-level values is safe.
var (
	userGasWei    = big.NewInt(1_000_000_000_000_000) // 0.001 ETH
	minUserGasWei = big.NewInt(200_000_000_000_000)   // 0.0002 ETH
)

// processDepositBridge funds a user's Hyperliquid account through the real
// Arbitrum bridge: the reserve positions native USDC (and gas) at the user's own
// Arbitrum address, that address deposits to Bridge2 (which credits the sending
// address on HL), and we wait for the HL credit before crediting the ledger.
func (e *Extension) processDepositBridge(a teetypes.Action, df *instruction.DataFixed) teetypes.ActionResult {
	user, payload, err := types.DecodeEnvelope(df.OriginalMessage)
	if err != nil {
		return buildResult(a, df, nil, 0, err)
	}
	var req types.DepositRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return buildResult(a, df, nil, 0, fmt.Errorf("decoding deposit: %w", err))
	}
	if req.UsdAmount <= 0 {
		return buildResult(a, df, nil, 0, fmt.Errorf("usdAmount must be positive"))
	}
	if req.UsdAmount > config.MaxDepositUSD {
		return buildResult(a, df, nil, 0, fmt.Errorf("deposit %.2f exceeds per-deposit cap %.2f", req.UsdAmount, config.MaxDepositUSD))
	}
	if e.led.Total()+req.UsdAmount > config.MaxTotalDepositUSD {
		return buildResult(a, df, nil, 0, fmt.Errorf("deposit exceeds global cap"))
	}

	priv, err := keys.DeriveUserKey(e.masterSeed, user)
	if err != nil {
		return buildResult(a, df, nil, 0, fmt.Errorf("deriving key: %w", err))
	}
	userAddr := crypto.PubkeyToAddress(priv.PublicKey)
	acct, err := e.accountFor(user)
	if err != nil {
		return buildResult(a, df, nil, 0, fmt.Errorf("deriving account: %w", err))
	}

	ctx := context.Background()
	units := arb.USDToUnits(req.UsdAmount)

	// 1. Ensure the user's Arbitrum address can pay for its own deposit tx.
	uEth, err := e.arb.ETHBalance(ctx, userAddr)
	if err != nil {
		return buildResult(a, df, nil, 0, fmt.Errorf("reading user gas: %w", err))
	}
	if uEth.Cmp(minUserGasWei) < 0 {
		if _, err := e.arb.SendETH(ctx, e.reserveKey, userAddr, userGasWei); err != nil {
			return buildResult(a, df, nil, 0, fmt.Errorf("funding user gas: %w", err))
		}
	}

	// 2. Reserve positions the deposit USDC at the user's own Arbitrum address
	//    (Bridge2 credits the sender, so it must originate from the user).
	if _, err := e.arb.TransferUSDC(ctx, e.reserveKey, e.usdcAddr, userAddr, units); err != nil {
		return buildResult(a, df, nil, 0, fmt.Errorf("funding user from reserve: %w", err))
	}
	if _, err := e.arb.WaitForUSDCBalance(ctx, e.usdcAddr, userAddr, units, 5*time.Second, reserveFundTimeout); err != nil {
		return buildResult(a, df, nil, 0, fmt.Errorf("waiting for reserve funding: %w", err))
	}

	// 3. Deposit to Bridge2 and wait for HL to credit the user's account.
	before, err := hlTotalUSD(ctx, acct)
	if err != nil {
		return buildResult(a, df, nil, 0, fmt.Errorf("reading HL balance: %w", err))
	}
	if _, err := e.arb.DepositToBridge(ctx, priv, e.usdcAddr, e.bridgeAddr, units); err != nil {
		return buildResult(a, df, nil, 0, fmt.Errorf("bridge deposit: %w", err))
	}
	if err := waitForHLCredit(ctx, acct, before+req.UsdAmount-depositCreditTolerance, hlCreditTimeout); err != nil {
		return buildResult(a, df, nil, 0, fmt.Errorf("waiting for HL credit: %w", err))
	}

	e.led.Credit(user, req.UsdAmount)
	resp := types.DepositResponse{HLAddress: acct.Address(), BalanceUsd: e.led.Balance(user)}
	data, _ := json.Marshal(resp)
	return buildResult(a, df, data, 1, nil)
}

// processWithdrawBridge returns funds via HL's withdraw3 to the Arbitrum reserve
// (the Arbitrum-side hub that recycles float / later bridges back to Flare). It
// debits the ledger first and refunds on failure.
func (e *Extension) processWithdrawBridge(a teetypes.Action, df *instruction.DataFixed) teetypes.ActionResult {
	user, payload, err := types.DecodeEnvelope(df.OriginalMessage)
	if err != nil {
		return buildResult(a, df, nil, 0, err)
	}
	var req types.WithdrawRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return buildResult(a, df, nil, 0, fmt.Errorf("decoding withdraw: %w", err))
	}
	if req.UsdAmount <= 0 {
		return buildResult(a, df, nil, 0, fmt.Errorf("usdAmount must be positive"))
	}
	if err := e.led.Debit(user, req.UsdAmount); err != nil {
		return buildResult(a, df, nil, 0, err)
	}
	acct, err := e.accountFor(user)
	if err != nil {
		e.led.Credit(user, req.UsdAmount) // refund on derivation failure
		return buildResult(a, df, nil, 0, err)
	}
	if err := acct.Withdraw(context.Background(), e.reserveAddr.Hex(), req.UsdAmount); err != nil {
		e.led.Credit(user, req.UsdAmount) // refund on HL failure
		return buildResult(a, df, nil, 0, fmt.Errorf("withdraw3: %w", err))
	}
	data, _ := json.Marshal(types.WithdrawResponse{BalanceUsd: e.led.Balance(user)})
	return buildResult(a, df, data, 1, nil)
}

// hlTotalUSD returns the account's total USDC across perp (Core) and spot,
// robust to whether a bridge deposit lands in perp or is reported in spot
// (unified mode).
func hlTotalUSD(ctx context.Context, a hl.Account) (float64, error) {
	pos, err := a.Positions(ctx)
	if err != nil {
		return 0, err
	}
	return pos.PerpAccountValueUSD + pos.SpotUSDC, nil
}

// waitForHLCredit polls the HL account until its total USDC reaches target or
// the timeout elapses.
func waitForHLCredit(ctx context.Context, a hl.Account, target float64, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		cur, err := hlTotalUSD(ctx, a)
		if err == nil && cur >= target {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out (target %.2f, last %.2f)", target, cur)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Second):
		}
	}
}
