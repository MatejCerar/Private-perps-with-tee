package keys

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"github.com/ethereum/go-ethereum/common"
)

// FileRegistry is a WalletRegistry backed by a JSON file mapping each Flare user
// to their provisioned TEE wallet (walletID, keyID). The provisioning tool
// (tools/cmd/provision-wallet) writes this file; the extension reads it in
// tee-wallet mode. Simple and durable — a production deployment would source the
// mapping from the on-chain wallet events instead.
type FileRegistry struct {
	path string
	mu   sync.RWMutex
	m    map[common.Address]walletEntry
}

type walletEntry struct {
	WalletID string `json:"walletId"` // 0x-prefixed 32-byte hex
	KeyID    uint64 `json:"keyId"`
}

// LoadFileRegistry reads the registry file (an empty registry if it does not
// exist yet, so the extension can boot before any user is provisioned).
func LoadFileRegistry(path string) (*FileRegistry, error) {
	r := &FileRegistry{path: path, m: map[common.Address]walletEntry{}}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return r, nil
	}
	if err != nil {
		return nil, err
	}
	raw := map[string]walletEntry{}
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("parsing wallet registry %s: %w", path, err)
	}
	for k, v := range raw {
		r.m[common.HexToAddress(k)] = v
	}
	return r, nil
}

func (r *FileRegistry) Lookup(user common.Address) (common.Hash, uint64, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.m[user]
	if !ok {
		return common.Hash{}, 0, false
	}
	return common.HexToHash(e.WalletID), e.KeyID, true
}

// Put records a user's provisioned wallet and persists the file (used by the
// provisioning tool; the extension only reads).
func (r *FileRegistry) Put(user common.Address, walletID common.Hash, keyID uint64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.m[user] = walletEntry{WalletID: walletID.Hex(), KeyID: keyID}
	raw := make(map[string]walletEntry, len(r.m))
	for k, v := range r.m {
		raw[k.Hex()] = v
	}
	b, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(r.path, b, 0600)
}
