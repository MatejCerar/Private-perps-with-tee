package extension

import (
	"fmt"

	"github.com/flare-foundation/go-flare-common/pkg/tee/instruction"
	teetypes "github.com/flare-foundation/tee-node/pkg/types"
)

var errNotImplemented = fmt.Errorf("not implemented")

func (e *Extension) processDeposit(a teetypes.Action, df *instruction.DataFixed) teetypes.ActionResult {
	return buildResult(a, df, nil, 0, errNotImplemented)
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
