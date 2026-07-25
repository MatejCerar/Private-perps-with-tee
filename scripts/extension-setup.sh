#!/usr/bin/env bash
# extension-setup.sh — Extension-specific setup that runs BEFORE Docker Compose.
#
# This hook runs between pre-build (contract deployment) and docker-up (starting
# the TEE). Use it for any setup whose output the extension needs at startup —
# for example:
#   - Deploying auxiliary contracts (ERC20 tokens, oracles, vaults)
#   - Writing config files the extension reads at init (pairs, feeds, pools)
#   - Minting test tokens, setting allowances, seeding initial state
#
# The following variables are available (sourced from .env + config/extension.env):
#
#   INSTRUCTION_SENDER   — your deployed InstructionSender contract address
#   EXTENSION_ID         — your extension's ID on the TeeExtensionRegistry
#   CHAIN_URL            — chain RPC endpoint
#   ADDRESSES_FILE       — path to deployed-addresses.json
#   DEPLOYMENT_PRIVATE_KEY — funded deployer key
#
# Example: deploy a helper contract and write its address to a config file
#
#   cd "$PROJECT_DIR/tools"
#   HELPER_ADDR=$(go run ./cmd/deploy-helper -a "$ADDRESSES_FILE" -c "$CHAIN_URL")
#   echo "HELPER_CONTRACT=$HELPER_ADDR" > "$PROJECT_DIR/config/helper.env"
#
# Why this matters:
#   The extension container reads config at startup. Anything it needs must be
#   written to disk BEFORE `docker compose up`. If you deploy contracts after
#   the container starts, you'll need to restart it — this hook prevents that.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

GREEN='\033[0;32m'; NC='\033[0m'
log() { echo -e "${GREEN}[extension-setup]${NC} $*"; }

# --- Load environment ---
if [[ -f "$PROJECT_DIR/.env" ]]; then
    set -a; source "$PROJECT_DIR/.env"; set +a
fi
if [[ -f "$PROJECT_DIR/config/extension.env" ]]; then
    source "$PROJECT_DIR/config/extension.env"
fi

# Resolve ADDRESSES_FILE to an absolute path so it survives the cd into tools/
if [[ -n "${ADDRESSES_FILE:-}" && "$ADDRESSES_FILE" != /* ]]; then
    ADDRESSES_FILE="$PROJECT_DIR/$ADDRESSES_FILE"
fi

log "EXTENSION_ID:       ${EXTENSION_ID:-<not set>}"
log "INSTRUCTION_SENDER: ${INSTRUCTION_SENDER:-<not set>}"
log "CHAIN_URL:          ${CHAIN_URL:-<not set>}"

# --- Vault-mode funding: deploy TestUSDC + Vault, wire them, and write the
#     vault-mode config the extension container reads at startup (env_file:
#     config/extension.env). Only runs when HL_FUNDING_MODE=vault.
if [[ "${HL_FUNDING_MODE:-treasury}" == "vault" ]]; then
    [[ -n "${INSTRUCTION_SENDER:-}" ]] || { echo "INSTRUCTION_SENDER not set (pre-build must run first)"; exit 1; }

    log "Deploying TestUSDC + Vault and wiring the InstructionSender (setVault)..."
    cd "$PROJECT_DIR/tools"
    DEPLOY_OUT=$(go run ./cmd/deploy-vault \
        -a "${ADDRESSES_FILE:-./config/coston/deployed-addresses.json}" \
        -c "${CHAIN_URL:-https://coston-api.flare.network/ext/C/rpc}" \
        -instructionSender "$INSTRUCTION_SENDER")
    echo "$DEPLOY_OUT"
    cd "$PROJECT_DIR"

    TESTUSDC_ADDRESS=$(echo "$DEPLOY_OUT" | grep -E '^TESTUSDC_ADDRESS=' | cut -d= -f2)
    VAULT_ADDRESS=$(echo "$DEPLOY_OUT" | grep -E '^VAULT_ADDRESS=' | cut -d= -f2)
    [[ -n "$VAULT_ADDRESS" && -n "$TESTUSDC_ADDRESS" ]] || { echo "deploy-vault did not return addresses"; exit 1; }

    # The container reads config/extension.env wholesale (docker-compose env_file).
    # HL_VAULT_AUTHORITY_KEY / ARB_RESERVE_KEY default to HL_TREASURY_KEY in the Go
    # config, and the Arbitrum addresses have Arb-Sepolia / HL-testnet defaults, so
    # only these four vars need writing here.
    cat >> "$PROJECT_DIR/config/extension.env" <<EOF
HL_FUNDING_MODE=vault
TESTUSDC_ADDRESS=$TESTUSDC_ADDRESS
VAULT_ADDRESS=$VAULT_ADDRESS
HL_BRIDGE2_MOCK=${HL_BRIDGE2_MOCK:-true}
EOF

    log "Vault mode wired: TestUSDC=$TESTUSDC_ADDRESS Vault=$VAULT_ADDRESS bridge2Mock=${HL_BRIDGE2_MOCK:-true}"
    log ""
    log "Frontend — set these in frontend/.env.local, then \`cd frontend && npm run dev\`:"
    log "  NEXT_PUBLIC_CHAIN=<coston|coston2, matching this setup>"
    log "  NEXT_PUBLIC_INSTRUCTION_SENDER=$INSTRUCTION_SENDER"
    log "  NEXT_PUBLIC_TESTUSDC=$TESTUSDC_ADDRESS"
    log "  NEXT_PUBLIC_VAULT=$VAULT_ADDRESS"
    log "  TEE_PROXY_URL=${EXT_PROXY_URL:-<your ext-proxy public url>}"
else
    log "HL_FUNDING_MODE is '${HL_FUNDING_MODE:-treasury}' (not 'vault') — skipping vault deploy."
fi
