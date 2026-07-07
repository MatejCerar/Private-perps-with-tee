// Package hl adapts github.com/sonirico/go-hyperliquid to a small Account
// interface so the extension's handlers can be unit-tested with fakes.
package hl

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strconv"
	"time"

	"github.com/ethereum/go-ethereum/signer/core/apitypes"
	gohl "github.com/sonirico/go-hyperliquid"
)

type OrderReq struct {
	Coin       string
	Market     string // "perp" | "spot"
	IsBuy      bool
	OrderType  string // "market" | "limit"
	Size       float64
	LimitPx    float64
	ReduceOnly bool
	Leverage   int
	IsCross    bool
}

type OrderResult struct {
	Raw     json.RawMessage
	OrderID int64
}

type Position struct {
	Coin          string  `json:"coin"`
	Szi           float64 `json:"szi"`
	EntryPx       float64 `json:"entryPx"`
	UnrealizedPnl float64 `json:"unrealizedPnl"`
}

type PositionsSnapshot struct {
	Perp                []Position `json:"perp"`
	PerpAccountValueUSD float64    `json:"perpAccountValueUsd"`
	SpotUSDC            float64    `json:"spotUsdc"`
}

type Account interface {
	Address() string
	PlaceOrder(ctx context.Context, req OrderReq) (OrderResult, error)
	Cancel(ctx context.Context, coin string, oid int64) error
	SendUSD(ctx context.Context, dest string, amount float64) error
	Withdraw(ctx context.Context, dest string, amount float64) error
	ClassTransfer(ctx context.Context, amount float64, toPerp bool) error
	SpotUSDC(ctx context.Context) (float64, error)
	PerpWithdrawable(ctx context.Context) (float64, error)
	SetLeverage(ctx context.Context, coin string, leverage int, isCross bool) error
	SetAbstraction(ctx context.Context, mode string) error
	Positions(ctx context.Context) (PositionsSnapshot, error)
}

// Abstraction modes for SetAbstraction. "disabled" = standard mode, which keeps
// separate spot/perp balances and (unlike unifiedAccount) permits usdSend,
// spotSend, and spot<->perp class transfers.
const (
	AbstractionStandard = "disabled"
	AbstractionUnified  = "unifiedAccount"
)

type Factory interface {
	Account(privKey *ecdsa.PrivateKey, accountAddr string) Account
}

type realFactory struct{ baseURL string }

func NewRealFactory(baseURL string) Factory { return &realFactory{baseURL: baseURL} }

func (f *realFactory) Account(privKey *ecdsa.PrivateKey, accountAddr string) Account {
	// Pass a sanitized spotMeta so go-hyperliquid does not fetch it itself and
	// panic on testnet. In SpotAssetInfo, Tokens[0] is a token *index*, but the
	// library uses it as a positional index into SpotMeta.Tokens; on testnet,
	// frozen/removed tokens leave gaps so some pairs reference an index past the
	// end of the Tokens slice (info.go:111). We drop those malformed pairs.
	spotMeta := sanitizedSpotMeta(f.baseURL)
	ex := gohl.NewExchange(context.Background(), privKey, f.baseURL, nil, "", accountAddr, spotMeta, nil)
	return &realAccount{ex: ex, addr: accountAddr, usdcToken: usdcTokenID(spotMeta), privKey: privKey, baseURL: f.baseURL}
}

// usdcTokenID returns the spotSend token identifier for USDC ("USDC:<tokenId>"),
// the format Hyperliquid's spotSend action expects. Falls back to the bare name
// if USDC is not found in the fetched spot metadata.
func usdcTokenID(sm *gohl.SpotMeta) string {
	if sm != nil {
		for _, t := range sm.Tokens {
			if t.Name == "USDC" {
				return "USDC:" + t.TokenID
			}
		}
	}
	return "USDC"
}

// sanitizedSpotMeta fetches /info spotMeta and removes universe entries whose
// first token index is out of range for the tokens slice. It always returns a
// non-nil value so NewExchange never falls back to its own crashing fetch.
func sanitizedSpotMeta(baseURL string) *gohl.SpotMeta {
	sm := &gohl.SpotMeta{}
	body, _ := json.Marshal(map[string]string{"type": "spotMeta"})
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Post(baseURL+"/info", "application/json", bytes.NewReader(body))
	if err != nil {
		return sm
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return sm
	}
	if err := json.Unmarshal(raw, sm); err != nil {
		return &gohl.SpotMeta{}
	}
	kept := sm.Universe[:0]
	for _, u := range sm.Universe {
		if len(u.Tokens) > 0 && u.Tokens[0] >= 0 && u.Tokens[0] < len(sm.Tokens) {
			kept = append(kept, u)
		}
	}
	sm.Universe = kept
	return sm
}

type realAccount struct {
	ex        *gohl.Exchange
	addr      string
	usdcToken string
	privKey   *ecdsa.PrivateKey
	baseURL   string
}

var _ Account = (*realAccount)(nil)
var _ Factory = (*realFactory)(nil)

func (a *realAccount) Address() string { return a.addr }

func (a *realAccount) PlaceOrder(ctx context.Context, req OrderReq) (OrderResult, error) {
	var (
		status gohl.OrderStatus
		err    error
	)
	switch {
	case req.OrderType == "market" && req.ReduceOnly:
		sz := req.Size
		status, err = a.ex.MarketClose(ctx, req.Coin, &sz, nil, 0.05, nil, nil)
	case req.OrderType == "market":
		status, err = a.ex.MarketOpen(ctx, req.Coin, req.IsBuy, req.Size, nil, 0.05, nil, nil)
	default:
		status, err = a.ex.Order(ctx, gohl.CreateOrderRequest{
			Coin:       req.Coin,
			IsBuy:      req.IsBuy,
			Price:      req.LimitPx,
			Size:       req.Size,
			ReduceOnly: req.ReduceOnly,
			OrderType:  gohl.OrderType{Limit: &gohl.LimitOrderType{Tif: gohl.TifGtc}},
		}, nil)
	}
	if err != nil {
		return OrderResult{}, err
	}
	if status.Error != nil {
		return OrderResult{}, fmt.Errorf("order rejected: %s", *status.Error)
	}
	raw, _ := json.Marshal(status)
	res := OrderResult{Raw: raw}
	if status.Resting != nil {
		res.OrderID = status.Resting.Oid
	} else if status.Filled != nil {
		res.OrderID = int64(status.Filled.Oid)
	}
	return res, nil
}

func (a *realAccount) Cancel(ctx context.Context, coin string, oid int64) error {
	_, err := a.ex.BulkCancel(ctx, []gohl.CancelOrderRequest{{Coin: coin, OrderID: oid}})
	return err
}

func (a *realAccount) SendUSD(ctx context.Context, dest string, amount float64) error {
	// Unified accounts hold a single USDC balance (spot == trading collateral),
	// so fund/withdraw via spotSend of the USDC token rather than usdSend, which
	// targets the legacy perp sub-balance and is rejected on unified accounts.
	// go-hyperliquid does not inspect the response status, so surface it here.
	resp, err := a.ex.SpotTransfer(ctx, amount, dest, a.usdcToken)
	if err != nil {
		return err
	}
	if resp != nil && resp.Status != "ok" {
		return fmt.Errorf("spotSend rejected: status=%q error=%q", resp.Status, resp.Error)
	}
	return nil
}

// Withdraw sends USDC from the account's HL balance to an Arbitrum address via
// the withdraw3 action. HL validators deliver native USDC on Arbitrum in
// ~3-4 minutes, minus a small flat fee. This is the real settlement path used
// to fund the Arbitrum reserve and to return funds to users (vs the v1 stub
// that spotSent back to a treasury on HL).
func (a *realAccount) Withdraw(ctx context.Context, dest string, amount float64) error {
	resp, err := a.ex.WithdrawFromBridge(ctx, amount, dest)
	if err != nil {
		return err
	}
	if resp != nil && resp.Status != "ok" {
		return fmt.Errorf("withdraw3 rejected: status=%q error=%q", resp.Status, resp.Error)
	}
	return nil
}

func (a *realAccount) ClassTransfer(ctx context.Context, amount float64, toPerp bool) error {
	resp, err := a.ex.UsdClassTransfer(ctx, amount, toPerp)
	if err != nil {
		return err
	}
	if resp != nil && resp.Status != "ok" {
		return fmt.Errorf("usdClassTransfer rejected: status=%q error=%q", resp.Status, resp.Error)
	}
	return nil
}

// SetAbstraction sets the account's abstraction mode via the userSetAbstraction
// action (not in go-hyperliquid, so sent raw). Use AbstractionStandard so the
// account permits transfers and class transfers. Rejected by HL if the account
// has open positions/orders.
func (a *realAccount) SetAbstraction(ctx context.Context, mode string) error {
	nonce := time.Now().UnixMilli()
	action := map[string]any{
		"type":        "userSetAbstraction",
		"user":        a.addr,
		"abstraction": mode,
		"nonce":       big.NewInt(nonce),
	}
	payloadTypes := []apitypes.Type{
		{Name: "hyperliquidChain", Type: "string"},
		{Name: "user", Type: "address"},
		{Name: "abstraction", Type: "string"},
		{Name: "nonce", Type: "uint64"},
	}
	sig, err := gohl.SignUserSignedAction(a.privKey, action, payloadTypes,
		"HyperliquidTransaction:UserSetAbstraction", a.baseURL == gohl.MainnetAPIURL)
	if err != nil {
		return err
	}
	payload := map[string]any{"action": action, "nonce": nonce, "signature": sig}
	b, _ := json.Marshal(payload)
	resp, err := http.Post(a.baseURL+"/exchange", "application/json", bytes.NewReader(b))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var r struct {
		Status   string          `json:"status"`
		Response json.RawMessage `json:"response"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return fmt.Errorf("setAbstraction: unparseable response: %s", string(body))
	}
	if r.Status != "ok" {
		return fmt.Errorf("setAbstraction rejected: %s", string(r.Response))
	}
	return nil
}

// PerpWithdrawable returns the account's free (withdrawable) perp USDC — funds
// not tied up as position margin. Used to sweep perp->spot before a spot order.
func (a *realAccount) PerpWithdrawable(ctx context.Context) (float64, error) {
	us, err := a.ex.Info().UserState(ctx, a.addr)
	if err != nil {
		return 0, err
	}
	return strconv.ParseFloat(us.Withdrawable, 64)
}

func (a *realAccount) SetLeverage(ctx context.Context, coin string, leverage int, isCross bool) error {
	_, err := a.ex.UpdateLeverage(ctx, leverage, coin, isCross)
	return err
}

func (a *realAccount) SpotUSDC(ctx context.Context) (float64, error) {
	st, err := a.ex.Info().SpotUserState(ctx, a.addr)
	if err != nil {
		return 0, err
	}
	for _, b := range st.Balances {
		if b.Coin == "USDC" {
			return strconv.ParseFloat(b.Total, 64)
		}
	}
	return 0, nil
}

func (a *realAccount) Positions(ctx context.Context) (PositionsSnapshot, error) {
	var snap PositionsSnapshot
	us, err := a.ex.Info().UserState(ctx, a.addr)
	if err != nil {
		return snap, err
	}
	snap.PerpAccountValueUSD, _ = strconv.ParseFloat(us.MarginSummary.AccountValue, 64)
	for _, ap := range us.AssetPositions {
		szi, _ := strconv.ParseFloat(ap.Position.Szi, 64)
		var entry float64
		if ap.Position.EntryPx != nil {
			entry, _ = strconv.ParseFloat(*ap.Position.EntryPx, 64)
		}
		pnl, _ := strconv.ParseFloat(ap.Position.UnrealizedPnl, 64)
		snap.Perp = append(snap.Perp, Position{Coin: ap.Position.Coin, Szi: szi, EntryPx: entry, UnrealizedPnl: pnl})
	}
	if spot, err := a.SpotUSDC(ctx); err == nil {
		snap.SpotUSDC = spot
	}
	return snap, nil
}
