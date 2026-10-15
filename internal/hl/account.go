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

	"extension-scaffold/internal/hl/hlsign"
	"extension-scaffold/internal/keys"

	"github.com/ethereum/go-ethereum/crypto"
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
	Status  string // "resting" (on the book) | "filled" (immediately) | "" (unknown)
}

type Position struct {
	Coin          string  `json:"coin"`
	Szi           float64 `json:"szi"`
	EntryPx       float64 `json:"entryPx"`
	UnrealizedPnl float64 `json:"unrealizedPnl"`
	Leverage      int     `json:"leverage"`
	MarginUsed    float64 `json:"marginUsed"`
	PositionValue float64 `json:"positionValue"`
	LiquidationPx float64 `json:"liquidationPx"`
}

type PositionsSnapshot struct {
	Perp                []Position `json:"perp"`
	PerpAccountValueUSD float64    `json:"perpAccountValueUsd"`
	SpotUSDC            float64    `json:"spotUsdc"`
}

// OpenOrder is a resting order on an account.
type OpenOrder struct {
	Coin    string  `json:"coin"`
	Side    string  `json:"side"` // "buy" | "sell"
	Sz      float64 `json:"sz"`
	LimitPx float64 `json:"limitPx"`
	Oid     int64   `json:"oid"`
}

// AccountSnapshot is the full read of one HL account used to build the frontend
// view (and to merge across shard accounts). Richer than PositionsSnapshot: it
// adds withdrawable and open orders.
type AccountSnapshot struct {
	SpotUSDC            float64
	PerpAccountValueUSD float64
	Withdrawable        float64
	Perp                []Position
	OpenOrders          []OpenOrder
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
	AccountSnapshot(ctx context.Context) (AccountSnapshot, error)
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

// SignerFactory builds accounts whose HL actions are signed via a keys.Signer
// (tee-wallet / PMW custody), reached over the sign server — no raw key in
// process. Selected by HL_KEY_MODE=tee-wallet.
type SignerFactory interface {
	AccountForSigner(signer keys.Signer) Account
}

// readOnlyKeyHex is a fixed throwaway key used ONLY to build go-hyperliquid's
// Exchange for READ queries (Info()) in tee-wallet mode. It never signs anything
// — all state-changing actions go through the signerExchange. Reads address the
// account by its own address, not this key's.
const readOnlyKeyHex = "1111111111111111111111111111111111111111111111111111111111111111"

type realFactory struct{ baseURL string }

func NewRealFactory(baseURL string) Factory { return &realFactory{baseURL: baseURL} }

type teeWalletFactory struct{ baseURL string }

// NewTeeWalletFactory returns a SignerFactory for tee-wallet custody mode.
func NewTeeWalletFactory(baseURL string) SignerFactory { return &teeWalletFactory{baseURL: baseURL} }

func (f *teeWalletFactory) AccountForSigner(signer keys.Signer) Account {
	addr := signer.Address().Hex()
	spotMeta := sanitizedSpotMeta(f.baseURL)
	// Read-only Exchange (throwaway key) for Info() queries; all writes go via sx.
	roKey, _ := crypto.HexToECDSA(readOnlyKeyHex)
	ex := gohl.NewExchange(context.Background(), roKey, f.baseURL, nil, "", addr, spotMeta, nil)
	sx := newSignerExchange(signer, addr, f.baseURL, ex.Info())
	return &realAccount{
		ex:        ex,
		addr:      addr,
		usdcToken: usdcTokenID(spotMeta),
		signer:    signer,
		sx:        sx,
		baseURL:   f.baseURL,
	}
}

func (f *realFactory) Account(privKey *ecdsa.PrivateKey, accountAddr string) Account {
	// Pass a sanitized spotMeta so go-hyperliquid does not fetch it itself and
	// panic on testnet. In SpotAssetInfo, Tokens[0] is a token *index*, but the
	// library uses it as a positional index into SpotMeta.Tokens; on testnet,
	// frozen/removed tokens leave gaps so some pairs reference an index past the
	// end of the Tokens slice (info.go:111). We drop those malformed pairs.
	spotMeta := sanitizedSpotMeta(f.baseURL)
	ex := gohl.NewExchange(context.Background(), privKey, f.baseURL, nil, "", accountAddr, spotMeta, nil)
	// signer wraps the same key. In seed mode it is the derived key (identical
	// signatures); a future tee-wallet factory injects a TeeWalletSigner so HL
	// actions can be signed without the raw key (C9). go-hyperliquid's Exchange
	// still needs the raw key for order signing until that path is reimplemented
	// on top of hlsign.
	return &realAccount{ex: ex, addr: accountAddr, usdcToken: usdcTokenID(spotMeta), privKey: privKey, signer: keys.NewRawSigner(privKey), baseURL: f.baseURL}
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
	signer    keys.Signer // signs HL actions; seed-derived today, TEE-custodied under C9
	baseURL   string

	// sx is non-nil in tee-wallet custody mode (HL_KEY_MODE=tee-wallet): all
	// state-changing actions (order/cancel/leverage/withdraw/transfers) are then
	// signed via the Signer and posted directly, since go-hyperliquid's Exchange
	// cannot sign without the raw key. Reads still go through a.ex.Info() (no key).
	sx *signerExchange
}

var _ Account = (*realAccount)(nil)
var _ Factory = (*realFactory)(nil)

func (a *realAccount) Address() string { return a.addr }

func (a *realAccount) PlaceOrder(ctx context.Context, req OrderReq) (OrderResult, error) {
	if a.sx != nil {
		return a.sx.placeOrder(ctx, req)
	}
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
		res.Status = "resting"
	} else if status.Filled != nil {
		res.OrderID = int64(status.Filled.Oid)
		res.Status = "filled"
	}
	return res, nil
}

func (a *realAccount) Cancel(ctx context.Context, coin string, oid int64) error {
	if a.sx != nil {
		return a.sx.cancel(ctx, coin, oid)
	}
	_, err := a.ex.BulkCancel(ctx, []gohl.CancelOrderRequest{{Coin: coin, OrderID: oid}})
	return err
}

func (a *realAccount) SendUSD(ctx context.Context, dest string, amount float64) error {
	if a.sx != nil {
		return a.sx.sendUSD(ctx, a.usdcToken, dest, amount)
	}
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
	if a.sx != nil {
		return a.sx.withdraw(ctx, dest, amount)
	}
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
	if a.sx != nil {
		return a.sx.classTransfer(ctx, amount, toPerp)
	}
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
	// Signed through the Signer (hlsign), so this action works with a
	// TEE-custodied key as well as the seed-derived key — byte-identical to
	// go-hyperliquid's own signing (verified in internal/hl/hlsign).
	sig, err := hlsign.SignUserAction(a.signer, action, payloadTypes,
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
	if a.sx != nil {
		return a.sx.setLeverage(ctx, coin, leverage, isCross)
	}
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
		snap.Perp = append(snap.Perp, mapPosition(ap.Position))
	}
	if spot, err := a.SpotUSDC(ctx); err == nil {
		snap.SpotUSDC = spot
	}
	return snap, nil
}

// mapPosition converts a go-hyperliquid position into our Position, parsing the
// string-encoded numeric fields (liquidationPx can be null for a flat account).
func mapPosition(p gohl.Position) Position {
	szi, _ := strconv.ParseFloat(p.Szi, 64)
	pnl, _ := strconv.ParseFloat(p.UnrealizedPnl, 64)
	marginUsed, _ := strconv.ParseFloat(p.MarginUsed, 64)
	positionValue, _ := strconv.ParseFloat(p.PositionValue, 64)
	var entry, liq float64
	if p.EntryPx != nil {
		entry, _ = strconv.ParseFloat(*p.EntryPx, 64)
	}
	if p.LiquidationPx != nil {
		liq, _ = strconv.ParseFloat(*p.LiquidationPx, 64)
	}
	return Position{
		Coin: p.Coin, Szi: szi, EntryPx: entry, UnrealizedPnl: pnl,
		Leverage: p.Leverage.Value, MarginUsed: marginUsed,
		PositionValue: positionValue, LiquidationPx: liq,
	}
}

func (a *realAccount) AccountSnapshot(ctx context.Context) (AccountSnapshot, error) {
	var snap AccountSnapshot
	us, err := a.ex.Info().UserState(ctx, a.addr)
	if err != nil {
		return snap, err
	}
	snap.PerpAccountValueUSD, _ = strconv.ParseFloat(us.MarginSummary.AccountValue, 64)
	snap.Withdrawable, _ = strconv.ParseFloat(us.Withdrawable, 64)
	for _, ap := range us.AssetPositions {
		snap.Perp = append(snap.Perp, mapPosition(ap.Position))
	}
	if spot, err := a.SpotUSDC(ctx); err == nil {
		snap.SpotUSDC = spot
	}
	if oo, err := a.ex.Info().OpenOrders(ctx, a.addr); err == nil {
		for _, o := range oo {
			side := "buy"
			if o.Side == "A" { // HL: "B"=bid/buy, "A"=ask/sell
				side = "sell"
			}
			snap.OpenOrders = append(snap.OpenOrders, OpenOrder{
				Coin: o.Coin, Side: side, Sz: o.Size, LimitPx: o.LimitPx, Oid: o.Oid,
			})
		}
	}
	return snap, nil
}
