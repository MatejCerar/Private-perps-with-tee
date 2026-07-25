package extension

import (
	"context"
	"encoding/json"
	"fmt"

	"extension-scaffold/internal/config"
	"extension-scaffold/internal/hl"
	"extension-scaffold/internal/ledger"
	"extension-scaffold/pkg/types"

	"github.com/flare-foundation/go-flare-common/pkg/tee/instruction"
	teetypes "github.com/flare-foundation/tee-node/pkg/types"
)

func (e *Extension) processDeposit(a teetypes.Action, df *instruction.DataFixed) teetypes.ActionResult {
	if e.fundingMode == "bridge" {
		return e.processDepositBridge(a, df)
	}
	if e.fundingMode == "vault" {
		return e.processDepositVault(a, df)
	}
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
		return buildResult(a, df, nil, 0, fmt.Errorf("deposit exceeds global treasury cap"))
	}

	acct, err := e.accountFor(user)
	if err != nil {
		return buildResult(a, df, nil, 0, fmt.Errorf("deriving account: %w", err))
	}
	if err := e.treasury.SendUSD(context.Background(), acct.Address(), req.UsdAmount); err != nil {
		return buildResult(a, df, nil, 0, fmt.Errorf("funding from treasury: %w", err))
	}
	e.led.Credit(user, req.UsdAmount)

	resp := types.DepositResponse{HLAddress: acct.Address(), BalanceUsd: e.led.Balance(user)}
	data, _ := json.Marshal(resp)
	return buildResult(a, df, data, 1, nil)
}

// processPlaceOrder handles PLACE_ORDER instructions for both spot and perp markets.
// For perp orders with Leverage>0 it calls SetLeverage before placing.
//
// Collateral routing depends on the funding mode:
//   - bridge: user accounts are unified (one collateral pool, set at first
//     deposit), so the order fills from the single bridge deposit directly — no
//     class transfer.
//   - treasury (legacy, standard mode): deposits land in spot via spotSend, so it
//     sweeps collateral to whichever side the order needs first. For market buys
//     the notional uses Size as a rough floor because the fill price is unknown
//     pre-trade (§10 "spot funding is approximate" — v1 acceptable; a follow-up
//     can fetch the mid price for a tighter estimate).
func (e *Extension) processPlaceOrder(a teetypes.Action, df *instruction.DataFixed) teetypes.ActionResult {
	user, payload, err := types.DecodeEnvelope(df.OriginalMessage)
	if err != nil {
		return buildResult(a, df, nil, 0, err)
	}
	var req types.PlaceOrderRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return buildResult(a, df, nil, 0, fmt.Errorf("decoding order: %w", err))
	}
	if req.Coin == "" || req.Size <= 0 {
		return buildResult(a, df, nil, 0, fmt.Errorf("coin and positive size required"))
	}
	if req.Market != "perp" && req.Market != "spot" {
		return buildResult(a, df, nil, 0, fmt.Errorf("market must be perp or spot"))
	}
	if req.OrderType != "market" && req.OrderType != "limit" {
		return buildResult(a, df, nil, 0, fmt.Errorf("orderType must be market or limit"))
	}

	acct, err := e.accountFor(user)
	if err != nil {
		return buildResult(a, df, nil, 0, err)
	}
	ctx := context.Background()

	// Set leverage up front for perp orders (both funding modes).
	if req.Market == "perp" && req.Leverage > 0 {
		if err := acct.SetLeverage(ctx, req.Coin, req.Leverage, req.IsCross); err != nil {
			return buildResult(a, df, nil, 0, fmt.Errorf("set leverage: %w", err))
		}
	}

	// Bridge-mode accounts are unified: spot and perp share one collateral pool,
	// so the order fills from the single (perp-landed) bridge deposit with no
	// sweep. Treasury (legacy) accounts run in standard mode with deposits in
	// spot, so sweep collateral to whichever side this order needs, above a small
	// dust threshold to avoid needless class transfers.
	if e.fundingMode != "bridge" {
		const dustUSD = 1.0
		if req.Market == "perp" {
			spot, err := acct.SpotUSDC(ctx)
			if err != nil {
				return buildResult(a, df, nil, 0, fmt.Errorf("read spot balance: %w", err))
			}
			if spot > dustUSD {
				if err := acct.ClassTransfer(ctx, spot, true); err != nil {
					return buildResult(a, df, nil, 0, fmt.Errorf("spot->perp transfer: %w", err))
				}
			}
		} else if req.IsBuy { // spot buy needs USDC in the spot wallet
			perp, err := acct.PerpWithdrawable(ctx)
			if err != nil {
				return buildResult(a, df, nil, 0, fmt.Errorf("read perp balance: %w", err))
			}
			if perp > dustUSD {
				if err := acct.ClassTransfer(ctx, perp, false); err != nil {
					return buildResult(a, df, nil, 0, fmt.Errorf("perp->spot transfer: %w", err))
				}
			}
		}
	}

	result, err := acct.PlaceOrder(ctx, hl.OrderReq{
		Coin: req.Coin, Market: req.Market, IsBuy: req.IsBuy,
		OrderType: req.OrderType, Size: req.Size, LimitPx: req.LimitPx,
		ReduceOnly: req.ReduceOnly, Leverage: req.Leverage, IsCross: req.IsCross,
	})
	if err != nil {
		return buildResult(a, df, nil, 0, fmt.Errorf("placing order: %w", err))
	}

	status := result.Status
	if status == "" {
		status = "submitted"
	}
	e.led.AddOrder(user, ledger.OrderRecord{
		InstructionID: df.InstructionID.Hex(), OrderID: result.OrderID,
		Coin: req.Coin, Market: req.Market,
		IsBuy: req.IsBuy, Size: req.Size, Status: status,
	})

	resp := types.OrderResponse{Accepted: true, OrderID: result.OrderID, Detail: json.RawMessage(result.Raw)}
	data, _ := json.Marshal(resp)
	return buildResult(a, df, data, 1, nil)
}

func (e *Extension) processCancel(a teetypes.Action, df *instruction.DataFixed) teetypes.ActionResult {
	user, payload, err := types.DecodeEnvelope(df.OriginalMessage)
	if err != nil {
		return buildResult(a, df, nil, 0, err)
	}
	var req types.CancelRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return buildResult(a, df, nil, 0, fmt.Errorf("decoding cancel: %w", err))
	}
	if req.Coin == "" || req.OrderID == 0 {
		return buildResult(a, df, nil, 0, fmt.Errorf("coin and orderId required"))
	}
	acct, err := e.accountFor(user)
	if err != nil {
		return buildResult(a, df, nil, 0, err)
	}
	if err := acct.Cancel(context.Background(), req.Coin, req.OrderID); err != nil {
		return buildResult(a, df, nil, 0, fmt.Errorf("cancelling: %w", err))
	}
	e.led.MarkCancelled(user, req.OrderID) // best-effort recent-orders reflection
	data, _ := json.Marshal(types.CancelResponse{Cancelled: true})
	return buildResult(a, df, data, 1, nil)
}

func (e *Extension) processWithdraw(a teetypes.Action, df *instruction.DataFixed) teetypes.ActionResult {
	if e.fundingMode == "bridge" {
		return e.processWithdrawBridge(a, df)
	}
	if e.fundingMode == "vault" {
		return e.processWithdrawVault(a, df)
	}
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
		// refund the ledger debit on derivation failure
		e.led.Credit(user, req.UsdAmount)
		return buildResult(a, df, nil, 0, err)
	}
	if err := acct.SendUSD(context.Background(), e.treasuryAddr, req.UsdAmount); err != nil {
		e.led.Credit(user, req.UsdAmount) // refund on HL failure
		return buildResult(a, df, nil, 0, fmt.Errorf("returning to treasury: %w", err))
	}
	data, _ := json.Marshal(types.WithdrawResponse{BalanceUsd: e.led.Balance(user)})
	return buildResult(a, df, data, 1, nil)
}
