package extension

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"sync"
	"time"

	"extension-scaffold/internal/arb"
	"extension-scaffold/internal/config"
	"extension-scaffold/internal/hl"
	"extension-scaffold/internal/keys"
	"extension-scaffold/internal/ledger"
	"extension-scaffold/pkg/types"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/flare-foundation/go-flare-common/pkg/logger"
	"github.com/flare-foundation/go-flare-common/pkg/tee/instruction"
	teetypes "github.com/flare-foundation/tee-node/pkg/types"
	teeutils "github.com/flare-foundation/tee-node/pkg/utils"

	"github.com/flare-foundation/tee-node/pkg/processorutils"
)

type Extension struct {
	mu       sync.RWMutex
	Server   *http.Server
	signPort int

	factory    hl.Factory
	masterSeed []byte
	led        *ledger.Ledger

	// keyer resolves each user's signing identity: SeedKeyer derives it from
	// masterSeed (today), TeeWalletKeyer resolves it to a TEE-custodied key over
	// the sign server (C9/PMW). Selected by HL_KEY_MODE.
	keyer keys.Keyer

	// signerFactory is non-nil in tee-wallet mode: accountFor builds HL accounts
	// whose actions are signed via the keyer (no raw key), instead of the
	// seed-key factory.
	signerFactory hl.SignerFactory

	fundingMode string // "treasury" | "bridge"

	// treasury-mode funding (legacy spotSend from a shared HL treasury)
	treasury     hl.Account
	treasuryAddr string

	// bridge-mode funding (real Arbitrum Bridge2 deposit + withdraw3)
	arb           ArbClient
	reserveKey    *ecdsa.PrivateKey
	reserveSigner keys.Signer // reserve key as a Signer for Arbitrum txs
	reserveAddr   common.Address
	usdcAddr      common.Address
	bridgeAddr    common.Address
	reserveHL     hl.Account // reserve's HL account, for auto-refilling Arbitrum USDC via withdraw3

	// vault-mode funding (Coston TestUSDC vault + central Bridge2 + spotSend).
	// In this mode `treasury` is the reserve's HL account (one key funds Bridge2 on
	// Arbitrum and spotSends on HL). `bridge2Mock` skips the real Arbitrum legs.
	vaultAddr   common.Address
	vaultAuthKey *ecdsa.PrivateKey
	bridge2Mock bool

	accounts map[common.Address]hl.Account // cache: flare addr -> HL account

	// shardAccounts caches split "shard" accounts (HL_SPLIT_DEPOSITS); shard 0 is
	// the primary account (== accounts[user]), shards >= 1 are fresh standalone
	// accounts. shardCount records how many shards a user has funded, for the
	// aggregated balance/positions read.
	shardAccounts map[shardKey]hl.Account
	shardCount    map[common.Address]int

	// userLocks serializes per-user ops (one in-flight deposit/withdraw/order
	// chain per user) so same-user txs don't race on the user's own Arbitrum
	// nonce and the ledger isn't mutated mid-reconcile. Reconciliation TryLocks
	// these and skips busy users.
	userLocks     sync.Map // common.Address -> *sync.Mutex
	reconcileStop chan struct{}

	// depositPool holds escrowed deposits for delayed/batched private funding
	// (HL_PRIVATE_DEPOSIT=true). Non-nil only when enabled with a shared treasury
	// (treasury or vault funding). When set, deposit handlers enqueue instead of
	// funding synchronously, and the batch funder funds matured batches.
	depositPool     *DepositPool
	batchFunderStop chan struct{}

	// withdrawPool is the mirror on the way out: a WITHDRAW debits the ledger and
	// enqueues here; the payout batcher unwinds the HL account to the treasury after
	// a randomized, batched delay, so the on-chain withdraw request and the HL
	// reclaim can't be time/amount-correlated. Same DepositPool mechanics (user,
	// denomination, delay). Non-nil whenever depositPool is.
	withdrawPool      *DepositPool
	payoutBatcherStop chan struct{}

	// unifiedSet records which bridge-mode user accounts have been switched to
	// HL unified mode (done once at first deposit). common.Address -> struct{}.
	unifiedSet sync.Map
}

// ArbClient is the Arbitrum-side surface the bridge funding path needs. Real
// deployments use *arb.Client; tests can substitute a fake. Sending methods take
// a keys.Signer so the sending key may be seed-derived (in-process) or
// TEE-custodied (sign server) without changing this interface.
type ArbClient interface {
	USDCBalance(ctx context.Context, token, addr common.Address) (*big.Int, error)
	ETHBalance(ctx context.Context, addr common.Address) (*big.Int, error)
	TransferUSDC(ctx context.Context, from keys.Signer, token, to common.Address, units *big.Int) (common.Hash, error)
	SendETH(ctx context.Context, from keys.Signer, to common.Address, wei *big.Int) (common.Hash, error)
	DepositToBridge(ctx context.Context, from keys.Signer, usdc, bridge common.Address, units *big.Int) (common.Hash, error)
	WaitForUSDCBalance(ctx context.Context, token, addr common.Address, min *big.Int, poll, timeout time.Duration) (*big.Int, error)
}

// New is the production constructor. It loads config and wires the funding
// path selected by HL_FUNDING_MODE ("treasury" legacy, or "bridge").
func New(extensionPort, signPort int) *Extension {
	if err := config.LoadHLConfig(); err != nil {
		panic(fmt.Sprintf("HL config: %v", err))
	}
	config.LoadArbConfig()
	seed, err := keys.MustHexSeed(config.MasterSeedHex)
	if err != nil {
		panic(fmt.Sprintf("master seed: %v", err))
	}
	factory := hl.NewRealFactory(config.HyperliquidURL)

	// Tee-wallet custody (C9/PMW): keys live in the TEE, reached over the sign
	// server; a file registry (written by tools/cmd/provision-wallet) maps users
	// to their provisioned wallet. teeWallet stays nil in seed mode.
	var teeKeyer keys.Keyer
	var teeFactory hl.SignerFactory
	if config.KeyMode == "tee-wallet" {
		reg, err := keys.LoadFileRegistry(config.WalletRegistryPath)
		if err != nil {
			panic(fmt.Sprintf("wallet registry: %v", err))
		}
		teeKeyer = keys.NewTeeWalletKeyer(config.SignURL, reg)
		teeFactory = hl.NewTeeWalletFactory(config.HyperliquidURL)
		logger.Infof("HL key mode: tee-wallet (sign server %s, registry %s)", config.SignURL, config.WalletRegistryPath)
	}

	apply := func(e *Extension) *Extension {
		if teeFactory != nil {
			e.keyer = teeKeyer
			e.signerFactory = teeFactory
		}
		return e
	}

	if config.FundingMode == "bridge" {
		ac, err := arb.New(context.Background(), config.ArbitrumRPCURL)
		if err != nil {
			panic(fmt.Sprintf("arbitrum client: %v", err))
		}
		reserveKey, err := crypto.HexToECDSA(config.ArbReserveKeyHex)
		if err != nil {
			panic(fmt.Sprintf("reserve key: %v", err))
		}
		e := apply(NewWithBridge(extensionPort, signPort, factory, seed, ac, reserveKey,
			common.HexToAddress(config.ArbUSDCAddress), common.HexToAddress(config.HLBridge2Address)))
		e.startReconciler()
		return e
	}

	if config.FundingMode == "vault" {
		ac, err := arb.New(context.Background(), config.ArbitrumRPCURL)
		if err != nil {
			panic(fmt.Sprintf("arbitrum client: %v", err))
		}
		reserveKey, err := crypto.HexToECDSA(config.ArbReserveKeyHex)
		if err != nil {
			panic(fmt.Sprintf("reserve key: %v", err))
		}
		authKey, err := crypto.HexToECDSA(config.VaultAuthorityKeyHex)
		if err != nil {
			panic(fmt.Sprintf("vault authority key: %v", err))
		}
		e := apply(NewWithBridge(extensionPort, signPort, factory, seed, ac, reserveKey,
			common.HexToAddress(config.ArbUSDCAddress), common.HexToAddress(config.HLBridge2Address)))
		e.fundingMode = "vault"
		// The reserve's HL account IS the treasury: one key funds Bridge2 on
		// Arbitrum and spotSends to users on HL.
		e.treasury = e.reserveHL
		e.treasuryAddr = e.reserveAddr.Hex()
		e.vaultAddr = common.HexToAddress(config.VaultAddress)
		e.vaultAuthKey = authKey
		e.bridge2Mock = config.Bridge2Mock
		// Standard mode so the treasury can spotSend (unified disables transfers).
		if err := e.treasury.SetAbstraction(context.Background(), hl.AbstractionStandard); err != nil {
			logger.Warnf("treasury SetAbstraction(standard): %v (ensure treasury is flat and standard)", err)
		}
		logger.Infof("HL funding mode: vault (bridge2Mock=%v, vault=%s)", e.bridge2Mock, e.vaultAddr.Hex())
		e.enablePrivateDeposit()
		return e
	}

	// treasury mode (legacy)
	treasuryKey, err := crypto.HexToECDSA(config.TreasuryKeyHex)
	if err != nil {
		panic(fmt.Sprintf("treasury key: %v", err))
	}
	treasuryAddr := crypto.PubkeyToAddress(treasuryKey.PublicKey).Hex()
	treasury := factory.Account(treasuryKey, treasuryAddr)

	// Best-effort: put the treasury in standard mode so it can spotSend to
	// per-user accounts (unified accounts disable inter-account transfers).
	// Idempotent once set; fails only if the treasury has open positions/orders,
	// which the operator resolves during setup.
	if err := treasury.SetAbstraction(context.Background(), hl.AbstractionStandard); err != nil {
		logger.Warnf("treasury SetAbstraction(standard): %v (ensure treasury is flat and in standard mode)", err)
	}

	et := NewWith(extensionPort, signPort, factory, treasury, treasuryAddr, seed)
	et.enablePrivateDeposit()
	return et
}

// enablePrivateDeposit turns on delayed/batched funding when HL_PRIVATE_DEPOSIT is
// set and a shared treasury exists to fund from. Idempotent; a no-op otherwise, so
// the original synchronous deposit path is kept. Only the production constructor
// (New) calls this; test constructors drive the deposit pool directly.
func (e *Extension) enablePrivateDeposit() {
	if !config.PrivateDeposit || e.treasury == nil || e.depositPool != nil {
		return
	}
	e.depositPool = NewDepositPool(
		time.Duration(config.FundDelayMinSeconds)*time.Second,
		time.Duration(config.FundDelayMaxSeconds)*time.Second,
		0,
	)
	e.withdrawPool = NewDepositPool(
		time.Duration(config.FundDelayMinSeconds)*time.Second,
		time.Duration(config.FundDelayMaxSeconds)*time.Second,
		0,
	)
	e.startBatchFunder()
	e.startPayoutBatcher()
	logger.Infof("private deposit+withdraw enabled: batchMin=%d, delay=[%ds,%ds], tick=%ds, denominations=%v",
		config.BatchMin, config.FundDelayMinSeconds, config.FundDelayMaxSeconds, config.FundTickSeconds, config.Denominations)
}

// NewWith is the test constructor for treasury (legacy) funding mode.
func NewWith(extensionPort, signPort int, f hl.Factory, treasury hl.Account, treasuryAddr string, seed []byte) *Extension {
	e := buildExtension(extensionPort, signPort, f, seed)
	e.fundingMode = "treasury"
	e.treasury = treasury
	e.treasuryAddr = treasuryAddr
	return e
}

// NewWithBridge builds an extension in bridge funding mode with injected deps
// (a real *arb.Client in production, a fake in tests).
func NewWithBridge(extensionPort, signPort int, f hl.Factory, seed []byte, ac ArbClient, reserveKey *ecdsa.PrivateKey, usdc, bridge common.Address) *Extension {
	e := buildExtension(extensionPort, signPort, f, seed)
	e.fundingMode = "bridge"
	e.arb = ac
	e.reserveKey = reserveKey
	e.reserveSigner = keys.NewRawSigner(reserveKey)
	e.reserveAddr = crypto.PubkeyToAddress(reserveKey.PublicKey)
	e.usdcAddr = usdc
	e.bridgeAddr = bridge
	e.reserveHL = f.Account(reserveKey, e.reserveAddr.Hex())
	return e
}

func buildExtension(extensionPort, signPort int, f hl.Factory, seed []byte) *Extension {
	e := &Extension{
		signPort:        signPort,
		factory:         f,
		masterSeed:      seed,
		keyer:           keys.NewSeedKeyer(seed),
		led:             ledger.New(),
		accounts:          map[common.Address]hl.Account{},
		shardAccounts:     map[shardKey]hl.Account{},
		shardCount:        map[common.Address]int{},
		reconcileStop:     make(chan struct{}),
		batchFunderStop:   make(chan struct{}),
		payoutBatcherStop: make(chan struct{}),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /state", e.stateHandler)
	mux.HandleFunc("POST /action", e.actionHandler)
	e.Server = &http.Server{Addr: fmt.Sprintf(":%d", extensionPort), Handler: mux}
	return e
}

// userMutex returns the per-user lock. The handlers Lock it (serialize same-user
// ops); Reconcile TryLocks it (skips busy users). LoadOrStore keeps one mutex per
// user address for the life of the Extension.
func (e *Extension) userMutex(user common.Address) *sync.Mutex {
	v, _ := e.userLocks.LoadOrStore(user, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// ensureUnified puts a bridge-mode user account into HL's unified account mode
// exactly once, right after its first deposit credits. Unified merges the spot
// and perp balances into a single collateral pool, so both spot and perp orders
// fill from the bridge deposit (which lands in perp/Core) without a
// usdClassTransfer sweep. It must run AFTER the first credit: HL rejects
// userSetAbstraction on a never-deposited account ("Must deposit before
// performing actions", observed live on testnet). Best-effort and idempotent: a
// failure is logged and retried on the next deposit rather than failing the
// deposit. The caller must hold the user lock.
func (e *Extension) ensureUnified(ctx context.Context, user common.Address, acct hl.Account) {
	if _, done := e.unifiedSet.Load(user); done {
		return
	}
	if err := acct.SetAbstraction(ctx, hl.AbstractionUnified); err != nil {
		logger.Warnf("SetAbstraction(unified) for %s: %v (will retry on next deposit)", user.Hex(), err)
		return
	}
	e.unifiedSet.Store(user, struct{}{})
}

// accountFor returns the cached (or newly derived) HL account for a Flare user.
// In tee-wallet mode it builds a signer-backed account (keys stay in the TEE);
// in seed mode it derives the key and builds a go-hyperliquid-backed account.
func (e *Extension) accountFor(user common.Address) (hl.Account, error) {
	e.mu.RLock()
	a, ok := e.accounts[user]
	e.mu.RUnlock()
	if ok {
		return a, nil
	}

	var acct hl.Account
	if e.signerFactory != nil {
		signer, err := e.keyer.UserSigner(user)
		if err != nil {
			return nil, fmt.Errorf("resolving tee-wallet signer: %w", err)
		}
		acct = e.signerFactory.AccountForSigner(signer)
	} else {
		priv, err := keys.DeriveUserKey(e.masterSeed, user)
		if err != nil {
			return nil, err
		}
		addr := crypto.PubkeyToAddress(priv.PublicKey).Hex()
		acct = e.factory.Account(priv, addr)
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if existing, ok := e.accounts[user]; ok {
		return existing, nil // another goroutine derived it first; converge on one instance
	}
	e.accounts[user] = acct
	return acct, nil
}

// accountForShard returns the user's shard-index account. Shard 0 is the primary
// account (identical to accountFor); shards >= 1 are fresh standalone accounts
// derived from the master seed, used when HL_SPLIT_DEPOSITS spreads a deposit
// across several accounts. Cached. In tee-wallet mode (no raw-key derivation)
// sharding is unavailable, so every shard maps to the primary account.
func (e *Extension) accountForShard(user common.Address, shard int) (hl.Account, error) {
	if shard <= 0 || e.signerFactory != nil {
		return e.accountFor(user)
	}
	key := shardKey{user, shard}
	e.mu.RLock()
	a, ok := e.shardAccounts[key]
	e.mu.RUnlock()
	if ok {
		return a, nil
	}
	priv, err := keys.DeriveShardKey(e.masterSeed, user, shard)
	if err != nil {
		return nil, err
	}
	addr := crypto.PubkeyToAddress(priv.PublicKey).Hex()
	acct := e.factory.Account(priv, addr)

	e.mu.Lock()
	defer e.mu.Unlock()
	if e.shardAccounts == nil {
		e.shardAccounts = map[shardKey]hl.Account{}
	}
	if existing, ok := e.shardAccounts[key]; ok {
		return existing, nil
	}
	e.shardAccounts[key] = acct
	return acct, nil
}

// noteShardCount records that the user now uses at least n shard accounts, so the
// aggregated read (Layer 3) knows how many shards to sum across.
func (e *Extension) noteShardCount(user common.Address, n int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.shardCount == nil {
		e.shardCount = map[common.Address]int{}
	}
	if n > e.shardCount[user] {
		e.shardCount[user] = n
	}
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
	case df.OPCommand == teeutils.ToHash(config.OPCommandConfirmFunding):
		return e.handleAsync(action, df, e.processConfirmFunding)
	default:
		return http.StatusNotImplemented, []byte(fmt.Sprintf("unsupported op command: %s", df.OPCommand.Hex()))
	}
}

// Stop halts the background reconciler goroutine (started by New when
// ReconcileIntervalSeconds > 0). Idempotent; safe to call even if the reconciler
// was never started. The HTTP server is stopped separately via e.Server.Shutdown.
func (e *Extension) Stop() {
	select {
	case <-e.reconcileStop:
		// already closed
	default:
		close(e.reconcileStop)
	}
	if e.batchFunderStop != nil {
		select {
		case <-e.batchFunderStop:
			// already closed
		default:
			close(e.batchFunderStop)
		}
	}
	if e.payoutBatcherStop != nil {
		select {
		case <-e.payoutBatcherStop:
			// already closed
		default:
			close(e.payoutBatcherStop)
		}
	}
}
