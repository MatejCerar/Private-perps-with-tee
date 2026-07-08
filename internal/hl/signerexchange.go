package hl

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"extension-scaffold/internal/hl/hlsign"
	"extension-scaffold/internal/keys"

	"github.com/ethereum/go-ethereum/signer/core/apitypes"
	gohl "github.com/sonirico/go-hyperliquid"
)

// signerExchange places Hyperliquid actions signed through a keys.Signer, for
// TEE-custodied keys (HL_KEY_MODE=tee-wallet) where go-hyperliquid's key-based
// Exchange cannot sign. It reuses go-hyperliquid's exported action wire types
// and a read-only Info (asset resolution, mids); only the signing leaf is
// swapped for hlsign, whose output is byte-identical to go-hyperliquid's own
// signing (verified in internal/hl/hlsign).
type signerExchange struct {
	signer    keys.Signer
	addr      string // the HL account address (== signer.Address())
	baseURL   string
	info      *gohl.Info
	isMainnet bool
	http      *http.Client

	nonceMu   sync.Mutex
	lastNonce int64
}

func newSignerExchange(signer keys.Signer, addr, baseURL string, info *gohl.Info) *signerExchange {
	return &signerExchange{
		signer:    signer,
		addr:      addr,
		baseURL:   baseURL,
		info:      info,
		isMainnet: baseURL == gohl.MainnetAPIURL,
		http:      &http.Client{Timeout: 20 * time.Second},
	}
}

// nextNonce returns a strictly increasing millisecond nonce (HL rejects
// non-increasing nonces from the same account), matching go-hyperliquid.
func (s *signerExchange) nextNonce() int64 {
	s.nonceMu.Lock()
	defer s.nonceMu.Unlock()
	n := time.Now().UnixMilli()
	if n <= s.lastNonce {
		n = s.lastNonce + 1
	}
	s.lastNonce = n
	return n
}

// postL1 signs an L1 action (order/cancel/updateLeverage) and posts it.
func (s *signerExchange) postL1(ctx context.Context, action any, out any) error {
	nonce := s.nextNonce()
	sig, err := hlsign.SignL1Action(s.signer, action, "", nonce, nil, s.isMainnet)
	if err != nil {
		return fmt.Errorf("sign L1 action: %w", err)
	}
	return s.post(ctx, action, sig, nonce, out)
}

// postUser signs a user-signed action (withdraw3/spotSend) and posts it. The
// on-the-wire nonce is the action's "time" field.
func (s *signerExchange) postUser(ctx context.Context, action map[string]any, payloadTypes []apitypes.Type, primaryType string, out any) error {
	nonce, _ := action["time"].(int64)
	sig, err := hlsign.SignUserAction(s.signer, action, payloadTypes, primaryType, s.isMainnet)
	if err != nil {
		return fmt.Errorf("sign user action: %w", err)
	}
	return s.post(ctx, action, sig, nonce, out)
}

func (s *signerExchange) post(ctx context.Context, action any, sig hlsign.SignatureResult, nonce int64, out any) error {
	payload := map[string]any{"action": action, "nonce": nonce, "signature": sig}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/exchange", bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("exchange %d: %s", resp.StatusCode, string(body))
	}
	if out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			return fmt.Errorf("decoding exchange response: %w (%s)", err, string(body))
		}
	}
	return nil
}

// placeOrder builds and signs an order action (limit, or market via an
// aggressive limit at mid±slippage) and returns the resting/filled result.
func (s *signerExchange) placeOrder(ctx context.Context, req OrderReq) (OrderResult, error) {
	asset, ok := s.info.CoinToAsset(req.Coin)
	if !ok {
		return OrderResult{}, fmt.Errorf("coin %s not found in info", req.Coin)
	}

	px := req.LimitPx
	tif := gohl.TifGtc
	if req.OrderType == "market" {
		// Aggressive IOC at mid ± 5% slippage, mirroring go-hyperliquid MarketOpen.
		mid, err := s.mid(ctx, req.Coin)
		if err != nil {
			return OrderResult{}, err
		}
		const slippage = 0.05
		if req.IsBuy {
			px = mid * (1 + slippage)
		} else {
			px = mid * (1 - slippage)
		}
		px = roundPx(px)
		tif = gohl.TifIoc
	}
	pxWire, err := floatToWire(px)
	if err != nil {
		return OrderResult{}, fmt.Errorf("price wire: %w", err)
	}
	szWire, err := floatToWire(req.Size)
	if err != nil {
		return OrderResult{}, fmt.Errorf("size wire: %w", err)
	}

	action := gohl.OrderAction{
		Type: "order",
		Orders: []gohl.OrderWire{{
			Asset:      asset,
			IsBuy:      req.IsBuy,
			LimitPx:    pxWire,
			Size:       szWire,
			ReduceOnly: req.ReduceOnly,
			OrderType:  gohl.OrderWireType{Limit: &gohl.OrderWireTypeLimit{Tif: tif}},
		}},
		Grouping: "na",
	}

	var result gohl.APIResponse[gohl.OrderResponse]
	if err := s.postL1(ctx, action, &result); err != nil {
		return OrderResult{}, err
	}
	if !result.Ok {
		return OrderResult{}, fmt.Errorf("order rejected: %s", result.Err)
	}
	if len(result.Data.Statuses) == 0 {
		return OrderResult{}, fmt.Errorf("order: no status returned")
	}
	st := result.Data.Statuses[0]
	if st.Error != nil {
		return OrderResult{}, fmt.Errorf("order rejected: %s", *st.Error)
	}
	raw, _ := json.Marshal(st)
	res := OrderResult{Raw: raw}
	if st.Resting != nil {
		res.OrderID = st.Resting.Oid
		res.Status = "resting"
	} else if st.Filled != nil {
		res.OrderID = int64(st.Filled.Oid)
		res.Status = "filled"
	}
	return res, nil
}

func (s *signerExchange) cancel(ctx context.Context, coin string, oid int64) error {
	asset, ok := s.info.CoinToAsset(coin)
	if !ok {
		return fmt.Errorf("coin %s not found in info", coin)
	}
	action := gohl.CancelAction{
		Type:    "cancel",
		Cancels: []gohl.CancelOrderWire{{Asset: asset, OrderID: oid}},
	}
	var result gohl.APIResponse[gohl.CancelResponse]
	if err := s.postL1(ctx, action, &result); err != nil {
		return err
	}
	if !result.Ok {
		return fmt.Errorf("cancel rejected: %s", result.Err)
	}
	return nil
}

func (s *signerExchange) setLeverage(ctx context.Context, coin string, leverage int, isCross bool) error {
	asset, ok := s.info.CoinToAsset(coin)
	if !ok {
		return fmt.Errorf("coin %s not found in info", coin)
	}
	action := gohl.UpdateLeverageAction{
		Type: "updateLeverage", Asset: asset, IsCross: isCross, Leverage: leverage,
	}
	return s.postL1(ctx, action, &json.RawMessage{})
}

// withdraw signs a withdraw3 (user-signed action) sending `amount` to `dest` on
// Arbitrum.
func (s *signerExchange) withdraw(ctx context.Context, dest string, amount float64) error {
	t := s.nextNonce()
	action := map[string]any{
		"type":        "withdraw3",
		"destination": strings.ToLower(dest),
		"amount":      strconv.FormatFloat(amount, 'f', -1, 64),
		"time":        t,
	}
	types := []apitypes.Type{
		{Name: "hyperliquidChain", Type: "string"}, {Name: "destination", Type: "string"},
		{Name: "amount", Type: "string"}, {Name: "time", Type: "uint64"},
	}
	var resp struct {
		Status string          `json:"status"`
		Resp   json.RawMessage `json:"response"`
	}
	if err := s.postUser(ctx, action, types, "HyperliquidTransaction:Withdraw", &resp); err != nil {
		return err
	}
	if resp.Status != "ok" {
		return fmt.Errorf("withdraw3 rejected: %s", string(resp.Resp))
	}
	return nil
}

// sendUSD signs a spotSend of the USDC token to `dest`.
func (s *signerExchange) sendUSD(ctx context.Context, usdcToken, dest string, amount float64) error {
	t := s.nextNonce()
	action := map[string]any{
		"type":        "spotSend",
		"destination": strings.ToLower(dest),
		"token":       usdcToken,
		"amount":      strconv.FormatFloat(amount, 'f', -1, 64),
		"time":        t,
	}
	types := []apitypes.Type{
		{Name: "hyperliquidChain", Type: "string"}, {Name: "destination", Type: "string"},
		{Name: "token", Type: "string"}, {Name: "amount", Type: "string"}, {Name: "time", Type: "uint64"},
	}
	var resp struct {
		Status string          `json:"status"`
		Resp   json.RawMessage `json:"response"`
	}
	if err := s.postUser(ctx, action, types, "HyperliquidTransaction:SpotSend", &resp); err != nil {
		return err
	}
	if resp.Status != "ok" {
		return fmt.Errorf("spotSend rejected: %s", string(resp.Resp))
	}
	return nil
}

// classTransfer signs a usdClassTransfer moving `amount` between spot and perp.
func (s *signerExchange) classTransfer(ctx context.Context, amount float64, toPerp bool) error {
	t := s.nextNonce()
	action := map[string]any{
		"type":   "usdClassTransfer",
		"amount": strconv.FormatFloat(amount, 'f', -1, 64),
		"toPerp": toPerp,
		"nonce":  t,
	}
	// usdClassTransfer's user-action nonce field is "nonce", not "time".
	types := []apitypes.Type{
		{Name: "hyperliquidChain", Type: "string"}, {Name: "amount", Type: "string"},
		{Name: "toPerp", Type: "bool"}, {Name: "nonce", Type: "uint64"},
	}
	var resp struct {
		Status string          `json:"status"`
		Resp   json.RawMessage `json:"response"`
	}
	sig, err := hlsign.SignUserAction(s.signer, action, types, "HyperliquidTransaction:UsdClassTransfer", s.isMainnet)
	if err != nil {
		return err
	}
	if err := s.post(ctx, action, sig, t, &resp); err != nil {
		return err
	}
	if resp.Status != "ok" {
		return fmt.Errorf("usdClassTransfer rejected: %s", string(resp.Resp))
	}
	return nil
}

func (s *signerExchange) mid(ctx context.Context, coin string) (float64, error) {
	mids, err := s.info.AllMids(ctx)
	if err != nil {
		return 0, err
	}
	v, ok := mids[coin]
	if !ok {
		return 0, fmt.Errorf("no mid for %s", coin)
	}
	return strconv.ParseFloat(v, 64)
}

// --- small helpers ---------------------------------------------------------

// floatToWire formats a float to HL's wire string (≤8 decimals, no trailing
// zeros), rejecting values that would lose precision. Reproduces
// go-hyperliquid's floatToWire so the signed action matches byte-for-byte.
func floatToWire(x float64) (string, error) {
	rounded := fmt.Sprintf("%.8f", x)
	parsed, err := strconv.ParseFloat(rounded, 64)
	if err != nil {
		return "", err
	}
	if math.Abs(parsed-x) >= 1e-12 {
		return "", fmt.Errorf("floatToWire loses precision: %f", x)
	}
	if rounded == "-0.00000000" {
		rounded = "0.00000000"
	}
	result := strings.TrimRight(rounded, "0")
	result = strings.TrimRight(result, ".")
	return result, nil
}

// roundPx rounds a market-order price to 5 significant figures (HL's max for
// perps), the same constraint the live tests price against.
func roundPx(x float64) float64 {
	if x == 0 {
		return 0
	}
	mag := math.Pow(10, 5-math.Ceil(math.Log10(math.Abs(x))))
	return math.Round(x*mag) / mag
}

