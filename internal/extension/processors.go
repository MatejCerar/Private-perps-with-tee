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

var errNotImplemented = fmt.Errorf("not implemented")

func (e *Extension) processDeposit(a teetypes.Action, df *instruction.DataFixed) teetypes.ActionResult {
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
// For spot buys it reads the current spot USDC balance and calls ClassTransfer(notional-spot, false)
// when the balance is short. For market buys the notional uses Size as a rough floor because the
// fill price is unknown pre-trade (§10 "spot funding is approximate" simplification — v1 acceptable;
// a follow-up can fetch the mid price for a tighter estimate).
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

	if req.Market == "perp" && req.Leverage > 0 {
		if err := acct.SetLeverage(ctx, req.Coin, req.Leverage, req.IsCross); err != nil {
			return buildResult(a, df, nil, 0, fmt.Errorf("set leverage: %w", err))
		}
	}

	if req.Market == "spot" && req.IsBuy {
		notional := req.Size * req.LimitPx
		if req.OrderType == "market" {
			notional = req.Size // px unknown; move size as a rough floor (see note)
		}
		spot, err := acct.SpotUSDC(ctx)
		if err != nil {
			return buildResult(a, df, nil, 0, fmt.Errorf("read spot balance: %w", err))
		}
		if spot < notional {
			if err := acct.ClassTransfer(ctx, notional-spot, false); err != nil {
				return buildResult(a, df, nil, 0, fmt.Errorf("perp->spot transfer: %w", err))
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

	e.led.AddOrder(user, ledger.OrderRecord{
		InstructionID: df.InstructionID.Hex(), Coin: req.Coin, Market: req.Market,
		IsBuy: req.IsBuy, Size: req.Size, Status: "submitted",
	})

	resp := types.OrderResponse{Accepted: true, OrderID: result.OrderID, Detail: json.RawMessage(result.Raw)}
	data, _ := json.Marshal(resp)
	return buildResult(a, df, data, 1, nil)
}

func (e *Extension) processCancel(a teetypes.Action, df *instruction.DataFixed) teetypes.ActionResult {
	return buildResult(a, df, nil, 0, errNotImplemented)
}

func (e *Extension) processWithdraw(a teetypes.Action, df *instruction.DataFixed) teetypes.ActionResult {
	return buildResult(a, df, nil, 0, errNotImplemented)
}
