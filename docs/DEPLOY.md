# Flare Private Perps — Deployment

This deploys the full product: the **frontend** on Cloudflare Pages and the
**backend** (TEE stack + relayer) on a server.

## Architecture (Direct-TEE mode)

```
Browser (Cloudflare Pages)
   │  1. on-chain deposit/withdraw/trade  → InstructionSender (Coston2)   [MetaMask, real escrow]
   │  2. deliver instruction              → relayer  (HTTPS)
   │  3. poll result                      → relayer  (HTTPS)
   ▼
Server
   relayer :8787 ──POST /action──▶ extension-tee :7702   (real enclave: derive HL acct,
   relayer ◀──GET /action/result── ext-proxy :6674        fund from treasury, trade, sign)
   ext-proxy ── reads signing policy ──▶ coston2 indexer DB (MySQL) ── syncs ──▶ Coston2
```

The enclave does all the real work (per-user HL account derivation, pooled/delayed
private funding, HL trading, result signing). We bypass the FTDC consensus relay
(which requires our tee-node/tee-proxy to exactly match the live `coston2-1`
version and have its attestation cosigned) by delivering instructions to the
enclave via a trusted local **relayer**. The on-chain escrow tx still runs, so
deposits are real and MetaMask-signed.

Versions that matter: `tee-proxy` is built from tag **v0.0.21** (its config needs
`initial_signing_policy_offset`); `tee-node` is pinned to commit `31fc839` by
`go.mod`. Do not bump these without re-testing.

---

## Part A — Backend on a server

Prereqs on the server: Docker + Docker Compose, Go 1.25+ (nix or distro),
`git`, and `cloudflared` (for a stable public tunnel). A funded Coston2 key for
the deployer/proxy signer, and a faucet-funded Hyperliquid testnet treasury.

### A1. Clone the repo and the e2e harness

```bash
git clone <your-fork-url> hype-accounts
cd hype-accounts
# e2e provides the local Coston2 indexer DB; it is a separate repo (not vendored).
git clone <e2e-repo-url> e2e
```

### A2. Apply the Coston2 indexer fixes (required)

The e2e indexer config ships stale values. Two edits in `e2e/docker/coston2/`:

1. `indexer-config.toml` — the **Relay** address was redeployed. Set it to the
   current one from the ContractRegistry:
   ```
   # [[indexer.collect_logs]] for the Relay
   contract_address = "0x5017728f117501a24ef9c3756c07f0d564598596"
   ```
   (Verify with `getContractAddressByName("Relay")` on the registry
   `0xaD67FE66660Fb8dFE9d6b1b4240d8650e30F6019` if it changes again.)

2. `indexer-entrypoint.sh` — start indexing ~2.5 reward epochs back (not at head)
   so the proxy can bootstrap the active signing policy. Add near the top:
   ```sh
   START_OFFSET_BLOCKS="${START_OFFSET_BLOCKS:-30000}"
   ```
   and write `start_index = head - START_OFFSET_BLOCKS` instead of `head`.

### A3. Fill the secrets (gitignored, never committed)

- `.env` (repo root): `CHAIN=coston2`, `CHAIN_ID=114`,
  `CHAIN_URL=https://coston2-api.flare.network/ext/C/rpc`, `LOCAL_MODE=false`,
  `SIMULATED_TEE=true`, `DEPLOYMENT_PRIVATE_KEY` / `PROXY_PRIVATE_KEY` (funded
  Coston2 key, no 0x), `INITIAL_OWNER`, `HL_MASTER_SEED`, `HL_TREASURY_KEY`,
  `HL_FUNDING_MODE=vault`, `HL_BRIDGE2_MOCK=true`, `HL_PRIVATE_DEPOSIT=true`,
  `HL_DENOMINATIONS=25,100,500`, and the deployed contract addresses
  (`INSTRUCTION_SENDER`, `VAULT_ADDRESS`, `TESTUSDC_ADDRESS`, `EXTENSION_ID`).
- `config/proxy/extension_proxy.coston2.docker.toml`: `[db] host =
  "coston2-indexer-db"` port 3306 db `db` root/root; `chain_id = 114`;
  `initial_signing_policy_offset = 0`; current Relay/FSM/VoterRegistry addresses.

The `docker-compose.selfcontained.yaml` override joins `ext-proxy` to the indexer
network (`coston2_default`), builds the enclave from this repo alone, and
publishes the extension action port (`127.0.0.1:7702`) for the relayer.

Contracts currently deployed on Coston2 (reuse or redeploy via
`scripts/pre-build.sh` + `deploy-vault`):

```
INSTRUCTION_SENDER = 0x20404965B1A9Be3aB7Bc1Dc8967f8A2A25167179
EXTENSION_ID       = 0x00000000000000000000000000000000000000000000000000000000000106c9
TESTUSDC           = 0x5C310B21416EE5315952C636a43732Ac9e671d6c
VAULT              = 0x3b9480f15D2fDEb41FC73e76f5f109cCAA2753ba
```

### A4. Build the tee-proxy v0.0.21 image

```bash
git clone https://github.com/flare-foundation/tee-proxy.git ~/tee-proxy-src
git -C ~/tee-proxy-src checkout v0.0.21
docker build -t local/tee-proxy ~/tee-proxy-src
```

### A5. Start the indexer, then the stack

```bash
# indexer DB + indexer (re-syncs from head-30000)
docker compose -f e2e/docker/coston2/docker-compose.yaml up -d
# wait until it reaches head, then confirm a signing policy is present:
docker exec coston2-coston2-indexer-db-1 mysql -uroot -proot db -e \
 "SELECT COUNT(*) FROM logs WHERE topic0='91d0280e969157fc6c5b8f952f237b03d934b18534dafcac839075bbc33522f8';"

# the TEE stack (enclave + proxy + redis)
CF="-f docker-compose.yaml -f docker-compose.coston2.yaml -f docker-compose.selfcontained.yaml"
docker compose $CF up -d --build
docker compose $CF logs --tail=30 ext-proxy    # expect "serving external at :6664", no panic
```

### A6. Run the relayer

```bash
cd tools
CGO_ENABLED=0 \
TESTUSDC_ADDRESS=0x5C310B21416EE5315952C636a43732Ac9e671d6c \
EXTENSION_URL=http://localhost:7702 \
PROXY_URL=http://localhost:6674 \
RELAYER_ADDR=:8787 \
go run ./cmd/relayer
```

Smoke test: `curl -s localhost:8787/tee/health`.

### A7. Expose the relayer publicly

The browser must reach the relayer over HTTPS. Use a named cloudflared tunnel
(stable hostname) or your own domain behind TLS:

```bash
cloudflared tunnel --url http://localhost:8787
# → https://<name>.trycloudflare.com   (quick tunnel; use a NAMED tunnel for a stable URL)
```

This public URL becomes `NEXT_PUBLIC_RELAYER_URL` for the frontend.

### A8. Seed the Hyperliquid treasury

The background funder draws from the treasury HL account. Fund `HL_TREASURY_KEY`'s
address on Hyperliquid **testnet** (activate it, then faucet/transfer USDC). Until
it holds USDC, deposits are accepted and enqueued but private funding cannot
complete.

---

## Part B — Frontend on Cloudflare Pages

1. Push this repo to the Git provider Cloudflare Pages reads from.
2. Cloudflare dashboard → **Workers & Pages → Create → Pages → Connect to Git**,
   select the repo.
3. Build settings:
   - Root directory: `frontend`
   - Build command: `npx @cloudflare/next-on-pages@1`
   - Build output directory: `.vercel/output/static`
   - (Simpler alternative: deploy the `frontend/` to **Vercel**, which runs
     Next.js natively — same env vars below.)
4. Environment variables (Production):
   - `NEXT_PUBLIC_CHAIN=coston2`
   - `NEXT_PUBLIC_INSTRUCTION_SENDER=0x20404965B1A9Be3aB7Bc1Dc8967f8A2A25167179`
   - `NEXT_PUBLIC_TESTUSDC=0x5C310B21416EE5315952C636a43732Ac9e671d6c`
   - `NEXT_PUBLIC_VAULT=0x3b9480f15D2fDEb41FC73e76f5f109cCAA2753ba`
   - `NEXT_PUBLIC_INSTRUCTION_FEE_WEI=1000000`
   - `NEXT_PUBLIC_RELAYER_URL=https://<your relayer tunnel>`   ← Part A7
   - `NEXT_PUBLIC_PRIVACY_MODE=true`
   - `NEXT_PUBLIC_DENOMINATIONS=25,100,500`
   - `NEXT_PUBLIC_MAX_DEPOSIT_USD=500`
5. Deploy. `NEXT_PUBLIC_*` are inlined at build time, so redeploy after changing
   any of them.

---

## Verify end to end

1. Open the Pages URL, connect MetaMask on Coston2.
2. Deposit a denomination (25/100/500): MetaMask signs the on-chain escrow; the
   relayer delivers to the enclave; the panel shows "funded privately in ~N min".
3. After the treasury funds the derived HL account, balance/positions appear.
4. Place a trade, then withdraw (TEE signs the release slip; `executeWithdraw`
   runs on Coston2).

Troubleshooting: `ext-proxy` panic on boot → signing policy not in the indexer DB
(re-check A2/A5). Relayer 502 → extension port 7702 not published or stack down.
Deposit stuck pending forever → treasury not seeded (A8).
