// Package hl adapts github.com/sonirico/go-hyperliquid to a small Account
// interface so the extension's handlers can be unit-tested with fakes.
package hl

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"strconv"

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
	ClassTransfer(ctx context.Context, amount float64, toPerp bool) error
	SpotUSDC(ctx context.Context) (float64, error)
	SetLeverage(ctx context.Context, coin string, leverage int, isCross bool) error
	Positions(ctx context.Context) (PositionsSnapshot, error)
}

type Factory interface {
	Account(privKey *ecdsa.PrivateKey, accountAddr string) Account
}

type realFactory struct{ baseURL string }

func NewRealFactory(baseURL string) Factory { return &realFactory{baseURL: baseURL} }

func (f *realFactory) Account(privKey *ecdsa.PrivateKey, accountAddr string) Account {
	ex := gohl.NewExchange(context.Background(), privKey, f.baseURL, nil, "", accountAddr, nil, nil)
	return &realAccount{ex: ex, addr: accountAddr}
}

type realAccount struct {
	ex   *gohl.Exchange
	addr string
}

var _ Account = (*realAccount)(nil)
var _ Factory = (*realFactory)(nil)

func (a *realAccount) Address() string { return a.addr }

func (a *realAccount) PlaceOrder(ctx context.Context, req OrderReq) (OrderResult, error) {
	var (
		status gohl.OrderStatus
		err    error
	)
	if req.OrderType == "market" {
		status, err = a.ex.MarketOpen(ctx, req.Coin, req.IsBuy, req.Size, nil, 0.05, nil, nil)
	} else {
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
	_, err := a.ex.UsdTransfer(ctx, amount, dest)
	return err
}

func (a *realAccount) ClassTransfer(ctx context.Context, amount float64, toPerp bool) error {
	_, err := a.ex.UsdClassTransfer(ctx, amount, toPerp)
	return err
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
