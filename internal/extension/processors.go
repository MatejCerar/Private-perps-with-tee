package extension

import (
	"context"
	"encoding/json"
	"fmt"

	"extension-scaffold/internal/config"
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

func (e *Extension) processPlaceOrder(a teetypes.Action, df *instruction.DataFixed) teetypes.ActionResult {
	return buildResult(a, df, nil, 0, errNotImplemented)
}

func (e *Extension) processCancel(a teetypes.Action, df *instruction.DataFixed) teetypes.ActionResult {
	return buildResult(a, df, nil, 0, errNotImplemented)
}

func (e *Extension) processWithdraw(a teetypes.Action, df *instruction.DataFixed) teetypes.ActionResult {
	return buildResult(a, df, nil, 0, errNotImplemented)
}
