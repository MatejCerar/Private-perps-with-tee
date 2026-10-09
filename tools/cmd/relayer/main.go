// relayer is the Direct-TEE bridge for Flare Private Perps.
//
// In the full FCC pipeline, on-chain InstructionSender events are relayed to the
// TEE by the data-provider consensus (FTDC). That path requires the live
// coston2-1 FTDC to cosign our enclave's attestation, which only works when our
// tee-node/tee-proxy match coston2-1 exactly. When that match isn't available,
// this relayer substitutes a trusted LOCAL relay: it builds the same instruction
// envelope the InstructionSender would and delivers it straight to the
// extension's /action endpoint. The enclave still does all the real work
// (account derivation, HL funding/trading, result signing); only the consensus
// relay is replaced.
//
// Flow: FE -> relayer (build DataFixed action) -> extension :7702/action
//       -> enclave processes async -> node signs -> proxy stores
//       -> FE polls relayer /tee/result/{id} -> proxy :6674/action/result/{id}
package main

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"math/big"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"

	cinstruction "github.com/flare-foundation/go-flare-common/pkg/tee/instruction"
	teetypes "github.com/flare-foundation/tee-node/pkg/types"
	teeutils "github.com/flare-foundation/tee-node/pkg/utils"
)

const usdcDecimals = 6

// OP constants mirror internal/config (kept as literals so this tool needs no
// internal imports). teeutils.ToHash reproduces Solidity's bytes32(string).
const (
	opTypeHL                = "HL"
	opCommandDeposit        = "DEPOSIT"
	opCommandPlaceOrder     = "PLACE_ORDER"
	opCommandCancel         = "CANCEL"
	opCommandWithdraw       = "WITHDRAW"
	opCommandConfirmFunding = "CONFIRM_FUNDING"
)

var (
	addrTy, _  = abi.NewType("address", "", nil)
	uintTy, _  = abi.NewType("uint256", "", nil)
	bytesTy, _ = abi.NewType("bytes", "", nil)

	// envelopeArgs = abi.encode(address user, bytes payload) (matches pkg/types).
	envelopeArgs = abi.Arguments{{Type: addrTy}, {Type: bytesTy}}
	// depositArgs = abi.encode(address token, uint256 amount, uint256 sponsoredLimit).
	depositArgs = abi.Arguments{{Type: addrTy}, {Type: uintTy}, {Type: uintTy}}
	// tokenAmountArgs = abi.encode(address token, uint256 amount) (withdraw/confirm).
	tokenAmountArgs = abi.Arguments{{Type: addrTy}, {Type: uintTy}}
)

type config struct {
	extensionURL string
	proxyURL     string
	addr         string
	testUSDC     common.Address
	allowOrigin  string
}

func loadConfig() config {
	return config{
		extensionURL: env("EXTENSION_URL", "http://localhost:7702"),
		proxyURL:     env("PROXY_URL", "http://localhost:6674"),
		addr:         env("RELAYER_ADDR", ":8787"),
		testUSDC:     common.HexToAddress(env("TESTUSDC_ADDRESS", "")),
		allowOrigin:  env("ALLOW_ORIGIN", "*"),
	}
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func usdToUnits(usd float64) *big.Int {
	return big.NewInt(int64(math.Round(usd * math.Pow10(usdcDecimals))))
}

func encodeEnvelope(user common.Address, payload []byte) ([]byte, error) {
	return envelopeArgs.Pack(user, payload)
}

// buildAction wraps (user, payload) in the exact DataFixed action the extension
// expects: OPType=HL, OPCommand=cmd, OriginalMessage=abi.encode(user,payload).
func buildAction(cmd string, user common.Address, payload []byte) (teetypes.Action, common.Hash, error) {
	msg, err := encodeEnvelope(user, payload)
	if err != nil {
		return teetypes.Action{}, common.Hash{}, fmt.Errorf("encode envelope: %w", err)
	}
	df := cinstruction.DataFixed{
		OPType:          teeutils.ToHash(opTypeHL),
		OPCommand:       teeutils.ToHash(cmd),
		OriginalMessage: msg,
	}
	dfJSON, err := json.Marshal(df)
	if err != nil {
		return teetypes.Action{}, common.Hash{}, fmt.Errorf("marshal datafixed: %w", err)
	}
	var id common.Hash
	if _, err := rand.Read(id[:]); err != nil {
		return teetypes.Action{}, common.Hash{}, err
	}
	a := teetypes.Action{
		Data: teetypes.ActionData{
			ID:            id,
			Type:          teetypes.Direct,
			SubmissionTag: teetypes.Submit,
			Message:       dfJSON,
		},
	}
	return a, id, nil
}

// submit posts the action to the extension and returns the action id.
func (c config) submit(a teetypes.Action) error {
	body, _ := json.Marshal(a)
	resp, err := http.Post(c.extensionURL+"/action", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("post to extension: %w", err)
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("extension returned %d: %s", resp.StatusCode, string(rb))
	}
	return nil
}

type apiError struct {
	Error string `json:"error"`
}

func main() {
	c := loadConfig()
	mux := http.NewServeMux()

	mux.HandleFunc("POST /tee/deposit", c.wrap(c.handleDeposit))
	mux.HandleFunc("POST /tee/withdraw", c.wrap(c.handleWithdraw))
	mux.HandleFunc("POST /tee/confirm", c.wrap(c.handleConfirm))
	mux.HandleFunc("POST /tee/order", c.wrap(c.handleOrder))
	mux.HandleFunc("POST /tee/cancel", c.wrap(c.handleCancel))
	mux.HandleFunc("GET /tee/result/{id}", c.wrap(c.handleResult))
	mux.HandleFunc("GET /tee/health", c.wrap(c.handleHealth))
	// CORS preflight for any /tee/* route.
	mux.HandleFunc("OPTIONS /tee/{rest...}", c.wrap(func(http.ResponseWriter, *http.Request) (any, int, error) {
		return map[string]string{"ok": "true"}, http.StatusOK, nil
	}))

	log.Printf("relayer listening on %s  extension=%s  proxy=%s  testUSDC=%s",
		c.addr, c.extensionURL, c.proxyURL, c.testUSDC.Hex())
	srv := &http.Server{Addr: c.addr, Handler: mux, ReadTimeout: 30 * time.Second, WriteTimeout: 60 * time.Second}
	log.Fatal(srv.ListenAndServe())
}

type handler func(w http.ResponseWriter, r *http.Request) (any, int, error)

func (c config) wrap(h handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", c.allowOrigin)
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Content-Type", "application/json")
		body, code, err := h(w, r)
		if err != nil {
			w.WriteHeader(code)
			_ = json.NewEncoder(w).Encode(apiError{Error: err.Error()})
			return
		}
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(body)
	}
}

func (c config) handleHealth(http.ResponseWriter, *http.Request) (any, int, error) {
	return map[string]string{"status": "ok", "extension": c.extensionURL, "proxy": c.proxyURL}, http.StatusOK, nil
}

type depositReq struct {
	User              string  `json:"user"`
	AmountUsd         float64 `json:"amountUsd"`
	SponsoredLimitUsd float64 `json:"sponsoredLimitUsd"`
}

func (c config) handleDeposit(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	var req depositReq
	if err := decode(r, &req); err != nil {
		return nil, http.StatusBadRequest, err
	}
	user, err := parseUser(req.User)
	if err != nil {
		return nil, http.StatusBadRequest, err
	}
	if c.testUSDC == (common.Address{}) {
		return nil, http.StatusInternalServerError, fmt.Errorf("TESTUSDC_ADDRESS not configured")
	}
	// Default the sponsored limit generously so the private (treasury-funded)
	// path is taken; the extension still enforces its own per/total caps.
	limit := req.SponsoredLimitUsd
	if limit <= 0 {
		limit = req.AmountUsd + 1_000_000
	}
	payload, err := depositArgs.Pack(c.testUSDC, usdToUnits(req.AmountUsd), usdToUnits(limit))
	if err != nil {
		return nil, http.StatusInternalServerError, fmt.Errorf("pack deposit: %w", err)
	}
	return c.submitCmd(opCommandDeposit, user, payload)
}

type amountReq struct {
	User      string  `json:"user"`
	AmountUsd float64 `json:"amountUsd"`
}

func (c config) handleWithdraw(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	return c.tokenAmountCmd(r, opCommandWithdraw)
}

func (c config) handleConfirm(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	return c.tokenAmountCmd(r, opCommandConfirmFunding)
}

func (c config) tokenAmountCmd(r *http.Request, cmd string) (any, int, error) {
	var req amountReq
	if err := decode(r, &req); err != nil {
		return nil, http.StatusBadRequest, err
	}
	user, err := parseUser(req.User)
	if err != nil {
		return nil, http.StatusBadRequest, err
	}
	if c.testUSDC == (common.Address{}) {
		return nil, http.StatusInternalServerError, fmt.Errorf("TESTUSDC_ADDRESS not configured")
	}
	payload, err := tokenAmountArgs.Pack(c.testUSDC, usdToUnits(req.AmountUsd))
	if err != nil {
		return nil, http.StatusInternalServerError, fmt.Errorf("pack %s: %w", cmd, err)
	}
	return c.submitCmd(cmd, user, payload)
}

type orderReq struct {
	User  string          `json:"user"`
	Order json.RawMessage `json:"order"`
}

func (c config) handleOrder(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	var req orderReq
	if err := decode(r, &req); err != nil {
		return nil, http.StatusBadRequest, err
	}
	user, err := parseUser(req.User)
	if err != nil {
		return nil, http.StatusBadRequest, err
	}
	if len(req.Order) == 0 {
		return nil, http.StatusBadRequest, fmt.Errorf("order required")
	}
	return c.submitCmd(opCommandPlaceOrder, user, []byte(req.Order))
}

type cancelReq struct {
	User   string          `json:"user"`
	Cancel json.RawMessage `json:"cancel"`
}

func (c config) handleCancel(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	var req cancelReq
	if err := decode(r, &req); err != nil {
		return nil, http.StatusBadRequest, err
	}
	user, err := parseUser(req.User)
	if err != nil {
		return nil, http.StatusBadRequest, err
	}
	if len(req.Cancel) == 0 {
		return nil, http.StatusBadRequest, fmt.Errorf("cancel required")
	}
	return c.submitCmd(opCommandCancel, user, []byte(req.Cancel))
}

func (c config) submitCmd(cmd string, user common.Address, payload []byte) (any, int, error) {
	a, id, err := buildAction(cmd, user, payload)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if err := c.submit(a); err != nil {
		return nil, http.StatusBadGateway, err
	}
	return map[string]string{"id": id.Hex(), "status": "pending"}, http.StatusOK, nil
}

// handleResult proxies the proxy's /action/result for the given id, trying each
// submission tag (the node may re-tag a result during signing).
func (c config) handleResult(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	id := r.PathValue("id")
	if !strings.HasPrefix(id, "0x") || len(id) != 66 {
		return nil, http.StatusBadRequest, fmt.Errorf("invalid action id")
	}
	for _, tag := range []string{"submit", "threshold", "end"} {
		url := fmt.Sprintf("%s/action/result/%s?submissionTag=%s", c.proxyURL, id, tag)
		resp, err := http.Get(url)
		if err != nil {
			return nil, http.StatusBadGateway, fmt.Errorf("proxy: %w", err)
		}
		rb, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			var out json.RawMessage = rb
			return out, http.StatusOK, nil
		}
	}
	return map[string]string{"status": "pending"}, http.StatusAccepted, nil
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("invalid body: %w", err)
	}
	return nil
}

func parseUser(s string) (common.Address, error) {
	if !common.IsHexAddress(s) {
		return common.Address{}, fmt.Errorf("invalid user address %q", s)
	}
	return common.HexToAddress(s), nil
}

var _ = hexutil.Encode // keep hexutil import for potential callers
