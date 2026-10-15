// Package types contains request/response types and the instruction envelope.
package types

import (
	"encoding/json"
	"fmt"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
)

// envelopeArgs describes abi.encode(address user, bytes payload).
var envelopeArgs abi.Arguments

func init() {
	addrTy, _ := abi.NewType("address", "", nil)
	bytesTy, _ := abi.NewType("bytes", "", nil)
	envelopeArgs = abi.Arguments{{Type: addrTy}, {Type: bytesTy}}
}

// DecodeEnvelope splits the on-chain instruction message into the calling
// user's address and the JSON payload.
func DecodeEnvelope(message []byte) (common.Address, []byte, error) {
	vals, err := envelopeArgs.Unpack(message)
	if err != nil {
		return common.Address{}, nil, fmt.Errorf("unpack envelope: %w", err)
	}
	if len(vals) != 2 {
		return common.Address{}, nil, fmt.Errorf("envelope: expected 2 fields, got %d", len(vals))
	}
	addr, ok := vals[0].(common.Address)
	if !ok {
		return common.Address{}, nil, fmt.Errorf("envelope: field 0 not address")
	}
	payload, ok := vals[1].([]byte)
	if !ok {
		return common.Address{}, nil, fmt.Errorf("envelope: field 1 not bytes")
	}
	return addr, payload, nil
}

// --- Requests (JSON payloads) ---

type DepositRequest struct {
	UsdAmount float64 `json:"usdAmount"`
}

type PlaceOrderRequest struct {
	Market     string  `json:"market"` // "perp" | "spot"
	Coin       string  `json:"coin"`
	IsBuy      bool    `json:"isBuy"`
	OrderType  string  `json:"orderType"` // "market" | "limit"
	Size       float64 `json:"size"`
	LimitPx    float64 `json:"limitPx"`
	ReduceOnly bool    `json:"reduceOnly"`
	Leverage   int     `json:"leverage"`
	IsCross    bool    `json:"isCross"`
}

type CancelRequest struct {
	Coin    string `json:"coin"`
	OrderID int64  `json:"orderId"`
}

type WithdrawRequest struct {
	UsdAmount float64 `json:"usdAmount"`
}

// --- Responses (ActionResult.Data) ---

type DepositResponse struct {
	HLAddress  string  `json:"hlAddress"`
	BalanceUsd float64 `json:"balanceUsd"`

	// Self-fund fields (present when the depositor is not sponsored): the user
	// must fund this HL address themselves (HL testnet faucet or Arbitrum bridge),
	// then call confirmFunding(token, amount) on the InstructionSender.
	SelfFund        bool   `json:"selfFund,omitempty"`
	SelfFundAddress string `json:"selfFundAddress,omitempty"`

	// Private deposit (HL_PRIVATE_DEPOSIT): when true, the deposit is escrowed and
	// the HL account is funded privately after a randomized, batched delay.
	// BalanceUsd stays 0 until funding lands; the frontend shows a
	// "funded privately in ~EstimatedFundingSeconds" state.
	PendingFunding          bool `json:"pendingFunding,omitempty"`
	EstimatedFundingSeconds int  `json:"estimatedFundingSeconds,omitempty"`

	// Split deposit (HL_SPLIT_DEPOSITS): how many shard accounts this deposit spread
	// across, the funded (denominable) total, and the snapped sub-denomination
	// remainder that was not funded.
	Shards       int     `json:"shards,omitempty"`
	FundedUsd    float64 `json:"fundedUsd,omitempty"`
	RemainderUsd float64 `json:"remainderUsd,omitempty"`
}

type OrderResponse struct {
	Accepted bool            `json:"accepted"`
	OrderID  int64           `json:"orderId"`
	Detail   json.RawMessage `json:"detail,omitempty"`
}

type CancelResponse struct {
	Cancelled bool `json:"cancelled"`
}

type WithdrawResponse struct {
	BalanceUsd float64 `json:"balanceUsd"`

	// Private withdraw (HL_PRIVATE_DEPOSIT): when true, the ledger is debited now and
	// the HL account is unwound to the treasury after a randomized, batched delay, so
	// the frontend shows a "paid out privately in ~EstimatedPayoutSeconds" state.
	PendingPayout          bool `json:"pendingPayout,omitempty"`
	EstimatedPayoutSeconds int  `json:"estimatedPayoutSeconds,omitempty"`

	// Vault withdrawal slip (present in "vault" funding mode): the user submits
	// these to Vault.executeWithdraw(user, token, amount, withdrawalId, signature)
	// on Coston to release the escrowed TestUSDC.
	Vault        string `json:"vault,omitempty"`
	User         string `json:"user,omitempty"`
	Token        string `json:"token,omitempty"`
	Amount       string `json:"amount,omitempty"`       // token units (decimal string)
	WithdrawalId string `json:"withdrawalId,omitempty"` // 0x-prefixed bytes32
	Signature    string `json:"signature,omitempty"`    // 0x-prefixed 65-byte EIP-191 sig
}

type ConfirmFundingResponse struct {
	HLAddress  string  `json:"hlAddress"`
	BalanceUsd float64 `json:"balanceUsd"`
}

// --- State (GET /state) ---

type AccountView struct {
	FlareAddress string      `json:"flareAddress"`
	HLAddress    string      `json:"hlAddress"`
	BalanceUsd   float64     `json:"balanceUsd"`
	RecentOrders interface{} `json:"recentOrders"`
}

type State struct {
	Accounts []AccountView `json:"accounts"`
}

// --- DO NOT MODIFY below this line. ---

type StateResponse struct {
	StateVersion common.Hash `json:"stateVersion"`
	State        State       `json:"state"`
}
