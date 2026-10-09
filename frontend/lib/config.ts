import { coston, coston2 } from "./chains";

// NEXT_PUBLIC_* vars are inlined at build time and safe to read on the client.
const chainName = (process.env.NEXT_PUBLIC_CHAIN ?? "coston").toLowerCase();

export const activeChain = chainName === "coston2" ? coston2 : coston;

export const INSTRUCTION_SENDER = (process.env.NEXT_PUBLIC_INSTRUCTION_SENDER ??
  "0x012bB7A094e044DA1fB6f7919f55A18f03304e57") as `0x${string}`;

// msg.value forwarded with every instruction. Mirrors tools/pkg/utils/instructions.go.
export const INSTRUCTION_FEE_WEI = BigInt(
  process.env.NEXT_PUBLIC_INSTRUCTION_FEE_WEI ?? "1000000",
);

// Sanity: a real, non-zero 20-byte address. When false, every write would
// revert with a cryptic error, so the UI surfaces a clear message instead.
export const INSTRUCTION_SENDER_OK =
  /^0x[0-9a-fA-F]{40}$/.test(INSTRUCTION_SENDER) &&
  INSTRUCTION_SENDER.toLowerCase() !== "0x0000000000000000000000000000000000000000";

// Per-wallet deposit cap enforced in the UI (backed by the extension's
// HL_MAX_DEPOSIT_USD). Testnet guardrail — worst case is treasury depletion.
export const MAX_DEPOSIT_USD = Number(
  process.env.NEXT_PUBLIC_MAX_DEPOSIT_USD ?? "100",
);

// Privacy mode (always on). Deposits go on-chain via the InstructionSender and
// the TEE funds the derived HL account privately after a short, randomized delay
// (surfaced as pendingFunding in the deposit result). Also gates the fixed
// deposit denominations below.
export const PRIVACY_MODE = true;

// Fixed deposit denominations (USD). In privacy mode deposits are restricted to
// these so individual amounts don't fingerprint a user across the batch. Read
// from NEXT_PUBLIC_DENOMINATIONS (comma-separated) or fall back to the default.
function parseDenominations(raw: string | undefined): number[] {
  if (!raw) return [25, 100, 500];
  const parsed = raw
    .split(",")
    .map((s) => Number(s.trim()))
    .filter((n) => Number.isFinite(n) && n > 0);
  return parsed.length > 0 ? parsed : [25, 100, 500];
}
export const DENOMINATIONS = parseDenominations(
  process.env.NEXT_PUBLIC_DENOMINATIONS,
);

// Snap an arbitrary amount to the nearest allowed denomination.
export function nearestDenomination(usd: number): number {
  return DENOMINATIONS.reduce((best, d) =>
    Math.abs(d - usd) < Math.abs(best - usd) ? d : best,
  );
}

const ZERO_ADDR = "0x0000000000000000000000000000000000000000";
const okAddr = (a: string) =>
  /^0x[0-9a-fA-F]{40}$/.test(a) && a.toLowerCase() !== ZERO_ADDR;

// Coston TestUSDC token (deposited into the vault) and the Vault (escrow +
// TEE-signed release). Both are filled after `deploy-vault`.
export const TESTUSDC = (process.env.NEXT_PUBLIC_TESTUSDC ?? ZERO_ADDR) as `0x${string}`;
export const VAULT = (process.env.NEXT_PUBLIC_VAULT ?? ZERO_ADDR) as `0x${string}`;
export const VAULT_CONFIGURED = okAddr(TESTUSDC) && okAddr(VAULT);

// TestUSDC has 6 decimals (like real USDC), so 1 USD = 1e6 units.
export const usdToUnits = (usd: number): bigint => BigInt(Math.round(usd * 1e6));
export const unitsToUsd = (units: bigint): number => Number(units) / 1e6;

export function explorerTx(hash: string): string {
  return `${activeChain.blockExplorers.default.url}/tx/${hash}`;
}

export function explorerAddress(addr: string): string {
  return `${activeChain.blockExplorers.default.url}/address/${addr}`;
}

// Public faucet for testnet gas (users pay the instruction fee in native token).
export function faucetUrl(): string {
  return activeChain.id === 114
    ? "https://faucet.flare.network/coston2"
    : "https://faucet.flare.network/coston";
}

// Hyperliquid explorer for the TEE-derived account (a different address from the
// Flare wallet). Testnet by default, matching the info API the app reads from;
// set NEXT_PUBLIC_HL_MAINNET=1 to point at mainnet.
const HL_MAINNET =
  process.env.NEXT_PUBLIC_HL_MAINNET === "1" ||
  process.env.NEXT_PUBLIC_HL_MAINNET === "true";

export function hlExplorerAddress(addr: string): string {
  const base = HL_MAINNET
    ? "https://app.hyperliquid.xyz"
    : "https://app.hyperliquid-testnet.xyz";
  return `${base}/explorer/address/${addr}`;
}
