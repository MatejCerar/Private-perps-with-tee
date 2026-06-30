package extension

import (
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"extension-scaffold/internal/config"
	"extension-scaffold/internal/hl"
	"extension-scaffold/internal/keys"
	"extension-scaffold/internal/ledger"
	"extension-scaffold/pkg/types"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/flare-foundation/go-flare-common/pkg/tee/instruction"
	teetypes "github.com/flare-foundation/tee-node/pkg/types"
	teeutils "github.com/flare-foundation/tee-node/pkg/utils"

	"github.com/flare-foundation/tee-node/pkg/processorutils"
)

type Extension struct {
	mu       sync.RWMutex
	Server   *http.Server
	signPort int

	factory      hl.Factory
	treasury     hl.Account
	treasuryAddr string
	masterSeed   []byte
	led          *ledger.Ledger

	accounts map[common.Address]hl.Account // cache: flare addr -> HL account
}

// New is the production constructor. It loads HL config and builds the
// treasury account from the configured key.
func New(extensionPort, signPort int) *Extension {
	if err := config.LoadHLConfig(); err != nil {
		panic(fmt.Sprintf("HL config: %v", err))
	}
	seed, err := keys.MustHexSeed(config.MasterSeedHex)
	if err != nil {
		panic(fmt.Sprintf("master seed: %v", err))
	}
	treasuryKey, err := crypto.HexToECDSA(config.TreasuryKeyHex)
	if err != nil {
		panic(fmt.Sprintf("treasury key: %v", err))
	}
	treasuryAddr := crypto.PubkeyToAddress(treasuryKey.PublicKey).Hex()
	factory := hl.NewRealFactory(config.HyperliquidURL)
	treasury := factory.Account(treasuryKey, treasuryAddr)

	return buildExtension(extensionPort, signPort, factory, treasury, treasuryAddr, seed)
}

// NewWith is the test constructor with injected dependencies.
func NewWith(extensionPort, signPort int, f hl.Factory, treasury hl.Account, treasuryAddr string, seed []byte) *Extension {
	return buildExtension(extensionPort, signPort, f, treasury, treasuryAddr, seed)
}

func buildExtension(extensionPort, signPort int, f hl.Factory, treasury hl.Account, treasuryAddr string, seed []byte) *Extension {
	e := &Extension{
		signPort:     signPort,
		factory:      f,
		treasury:     treasury,
		treasuryAddr: treasuryAddr,
		masterSeed:   seed,
		led:          ledger.New(),
		accounts:     map[common.Address]hl.Account{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /state", e.stateHandler)
	mux.HandleFunc("POST /action", e.actionHandler)
	e.Server = &http.Server{Addr: fmt.Sprintf(":%d", extensionPort), Handler: mux}
	return e
}

// accountFor returns the cached (or newly derived) HL account for a Flare user.
func (e *Extension) accountFor(user common.Address) (hl.Account, error) {
	e.mu.RLock()
	a, ok := e.accounts[user]
	e.mu.RUnlock()
	if ok {
		return a, nil
	}
	var priv *ecdsa.PrivateKey
	priv, err := keys.DeriveUserKey(e.masterSeed, user)
	if err != nil {
		return nil, err
	}
	addr := crypto.PubkeyToAddress(priv.PublicKey).Hex()
	acct := e.factory.Account(priv, addr)

	e.mu.Lock()
	defer e.mu.Unlock()
	if existing, ok := e.accounts[user]; ok {
		return existing, nil // another goroutine derived it first; converge on one instance
	}
	e.accounts[user] = acct
	return acct, nil
}

func (e *Extension) stateHandler(w http.ResponseWriter, r *http.Request) {
	e.mu.RLock()
	views := make([]types.AccountView, 0, len(e.accounts))
	for flareAddr, acct := range e.accounts {
		snap := e.led.SnapshotFor(flareAddr)
		views = append(views, types.AccountView{
			FlareAddress: flareAddr.Hex(),
			HLAddress:    acct.Address(),
			BalanceUsd:   snap.BalanceUSD,
			RecentOrders: snap.RecentOrders,
		})
	}
	e.mu.RUnlock()

	resp := types.StateResponse{
		StateVersion: teeutils.ToHash(config.Version),
		State:        types.State{Accounts: views},
	}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		http.Error(w, fmt.Sprintf("sending response: %v", err), http.StatusInternalServerError)
	}
}

func (e *Extension) processAction(action teetypes.Action) (int, []byte) {
	df, err := processorutils.Parse[instruction.DataFixed](action.Data.Message)
	if err != nil {
		return http.StatusBadRequest, []byte(fmt.Sprintf("decoding fixed data: %v", err))
	}
	if df.OPType != teeutils.ToHash(config.OPTypeHL) {
		return http.StatusNotImplemented, []byte(fmt.Sprintf("unsupported op type: %s", df.OPType.Hex()))
	}
	switch {
	case df.OPCommand == teeutils.ToHash(config.OPCommandDeposit):
		return e.handleAsync(action, df, e.processDeposit)
	case df.OPCommand == teeutils.ToHash(config.OPCommandPlaceOrder):
		return e.handleAsync(action, df, e.processPlaceOrder)
	case df.OPCommand == teeutils.ToHash(config.OPCommandCancel):
		return e.handleAsync(action, df, e.processCancel)
	case df.OPCommand == teeutils.ToHash(config.OPCommandWithdraw):
		return e.handleAsync(action, df, e.processWithdraw)
	default:
		return http.StatusNotImplemented, []byte(fmt.Sprintf("unsupported op command: %s", df.OPCommand.Hex()))
	}
}
