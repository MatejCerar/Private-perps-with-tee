"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  useAccount,
  useConnect,
  useDisconnect,
  usePublicClient,
  useSwitchChain,
  useWriteContract,
} from "wagmi";
import { injected } from "wagmi/connectors";
import { stringToHex } from "viem";
import { instructionSenderAbi, testUsdcAbi, vaultAbi } from "@/lib/abi";
import {
  activeChain,
  INSTRUCTION_FEE_WEI,
  INSTRUCTION_SENDER,
  INSTRUCTION_SENDER_OK,
  MAX_DEPOSIT_USD,
  TESTUSDC,
  VAULT,
  VAULT_CONFIGURED,
  usdToUnits,
} from "@/lib/config";
import { decodeResultData, extractInstructionId, pollResult } from "@/lib/tee";
import {
  fetchAccount,
  fetchMids,
  fetchSzDecimals,
  type HlAccount,
} from "@/lib/hl";
import { css, C, num, usd, truncAddr } from "@/lib/ui";
import { marketBy } from "@/lib/markets";
import { Header, Banner } from "@/components/Header";
import { Hero, Onboarding } from "@/components/Hero";
import { TradeCard, type TradePayload } from "@/components/TradeCard";
import { AccountColumn } from "@/components/AccountColumn";
import { TxModal } from "@/components/TxModal";
import { StatusToast, type RunKind, type RunState } from "@/components/StatusToast";
import { SelfFundPanel } from "@/components/SelfFundPanel";
import { AdminPanel } from "@/components/AdminPanel";

type Fn = "placeOrder" | "cancel";

function friendlyError(e: unknown): string {
  const m = e instanceof Error ? e.message : String(e);
  if (/user rejected|rejected the request|denied|user denied/i.test(m))
    return "You rejected the request in your wallet.";
  if (/insufficient funds/i.test(m)) return "Not enough gas — grab some from the faucet.";
  return m.split("\n")[0].slice(0, 240);
}

function round(v: number, decimals: number): number {
  const f = Math.pow(10, decimals);
  return Math.round(v * f) / f;
}

export default function Home() {
  const { address, isConnected, chainId } = useAccount();
  const { connectAsync } = useConnect();
  const { disconnect } = useDisconnect();
  const { switchChain } = useSwitchChain();
  const { writeContractAsync } = useWriteContract();
  const publicClient = usePublicClient();

  // ---- state ----
  const [hlAddress, setHlAddress] = useState<string | undefined>(undefined);
  const [account, setAccount] = useState<HlAccount | null>(null);
  const [mids, setMids] = useState<Record<string, number>>({});
  const szDecimals = useRef<Record<string, number>>({});

  const [market, setMarket] = useState("ETH");
  const [marketOpen, setMarketOpen] = useState(false);
  const [direction, setDirection] = useState<"long" | "short">("long");
  const [pay, setPay] = useState("50");
  const [leverage, setLeverage] = useState(2);
  const [advancedOpen, setAdvancedOpen] = useState(false);
  const [useLimit, setUseLimit] = useState(false);
  const [limitPrice, setLimitPrice] = useState("");
  const [reduceOnly, setReduceOnly] = useState(false);

  const [modal, setModal] = useState<{ kind: "deposit" | "withdraw"; amount: string } | null>(null);
  const [showOnboarding, setShowOnboarding] = useState(false);
  const [connecting, setConnecting] = useState(false);
  const [connectError, setConnectError] = useState<string | undefined>(undefined);
  const [hasWallet, setHasWallet] = useState(true); // assume yes until we check (avoids SSR flash)
  const [run, setRun] = useState<RunState | null>(null);
  const [pendingCoins, setPendingCoins] = useState<Set<string>>(new Set());
  const [refreshNonce, setRefreshNonce] = useState(0);

  // Self-fund state: set when a non-sponsored deposit returns selfFund:true.
  // The user must fund the HL address via the HL testnet faucet, then confirm.
  const [selfFund, setSelfFund] = useState<{
    address: string;
    amountUnits: bigint;
    amountUsd: number;
  } | null>(null);

  const dismissTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  // ---- derived ----
  const available = account ? account.spotUsdc : 0;
  const inPositions = account ? account.perpAccountValue : 0;
  const balance = available + inPositions;
  const withdrawable = account ? account.spotUsdc + account.withdrawable : balance;
  const everDeposited = !!hlAddress || balance > 0 || (account?.positions.length ?? 0) > 0;
  const busy =
    !!run &&
    (run.phase === "signing" ||
      run.phase === "mining" ||
      run.phase === "relaying" ||
      run.phase === "releasing");
  const wrongNetwork = isConnected && chainId !== activeChain.id;
  const existingPosition = useMemo(() => {
    const p = account?.positions.find((x) => x.coin === market);
    if (!p) return undefined;
    return { coin: p.coin, isLong: p.szi >= 0, size: Math.abs(p.szi), leverage: p.leverage };
  }, [account, market]);

  // ---- detect an injected wallet (client-only) ----
  useEffect(() => {
    setHasWallet(typeof window !== "undefined" && "ethereum" in window);
  }, []);

  // ---- remember derived HL address per wallet ----
  useEffect(() => {
    if (!address) {
      setHlAddress(undefined);
      setAccount(null);
      return;
    }
    setHlAddress(localStorage.getItem(`hl:${address.toLowerCase()}`) ?? undefined);
  }, [address]);

  // ---- poll live mids + fetch szDecimals once (public data) ----
  useEffect(() => {
    if (!isConnected) return;
    let alive = true;
    const load = () =>
      fetchMids()
        .then((m) => alive && setMids(m))
        .catch(() => {});
    load();
    fetchSzDecimals()
      .then((d) => (szDecimals.current = d))
      .catch(() => {});
    const t = setInterval(load, 2500);
    return () => {
      alive = false;
      clearInterval(t);
    };
  }, [isConnected]);

  // ---- poll the derived HL account ----
  const loadAccount = useCallback(async () => {
    if (!hlAddress) return;
    try {
      setAccount(await fetchAccount(hlAddress));
    } catch {
      /* transient — keep last */
    }
  }, [hlAddress]);

  useEffect(() => {
    if (!hlAddress) return;
    loadAccount();
    const t = setInterval(loadAccount, 5000);
    return () => clearInterval(t);
  }, [hlAddress, loadAccount, refreshNonce]);

  // ---- animated balance count-up ----
  const [displayBalance, setDisplayBalance] = useState(0);
  const displayRef = useRef(0);
  const rafRef = useRef<number | null>(null);
  const reduceMotion = useRef(false);
  useEffect(() => {
    reduceMotion.current =
      typeof window !== "undefined" &&
      window.matchMedia &&
      window.matchMedia("(prefers-reduced-motion: reduce)").matches;
  }, []);
  useEffect(() => {
    const from = displayRef.current;
    const to = balance;
    if (reduceMotion.current || from === to) {
      displayRef.current = to;
      setDisplayBalance(to);
      return;
    }
    if (rafRef.current) cancelAnimationFrame(rafRef.current);
    const start = performance.now();
    const dur = 850;
    const step = (now: number) => {
      const t = Math.min(1, (now - start) / dur);
      const e = 1 - Math.pow(1 - t, 3);
      displayRef.current = t < 1 ? from + (to - from) * e : to;
      setDisplayBalance(displayRef.current);
      if (t < 1) rafRef.current = requestAnimationFrame(step);
    };
    rafRef.current = requestAnimationFrame(step);
    return () => {
      if (rafRef.current) cancelAnimationFrame(rafRef.current);
    };
  }, [balance]);

  useEffect(() => () => {
    if (dismissTimer.current) clearTimeout(dismissTimer.current);
  }, []);

  // ---- the async instruction pipeline (drives the status toast) ----
  // `writeCall` submits the tracked on-chain instruction and returns its hash;
  // any prep txs (e.g. approve) run inside it before the returned hash. `onSlip`
  // runs an extra on-chain step after the TEE result (withdraw → executeWithdraw).
  const runInstruction = useCallback(
    async (
      writeCall: () => Promise<`0x${string}`>,
      meta: { kind: RunKind; title: string; sub: string },
      opts?: {
        onSlip?: (decoded: unknown) => Promise<void>;
        onDecoded?: (decoded: unknown) => void;
      },
    ) => {
      if (!publicClient || busy) return;
      if (dismissTimer.current) clearTimeout(dismissTimer.current);
      if (!INSTRUCTION_SENDER_OK) {
        setRun({
          phase: "error",
          stepReached: 0,
          detailsOpen: false,
          ...meta,
          error: "Contract address not configured — set NEXT_PUBLIC_INSTRUCTION_SENDER.",
        });
        return;
      }
      setRun({ phase: "signing", stepReached: 0, detailsOpen: false, ...meta });
      try {
        const hash = await writeCall();
        setRun((s) => (s ? { ...s, phase: "mining", stepReached: 1, txHash: hash } : s));

        const receipt = await publicClient.waitForTransactionReceipt({ hash });
        if (receipt.status !== "success") throw new Error("transaction reverted on-chain");

        const instructionId = extractInstructionId(receipt);
        setRun((s) => (s ? { ...s, phase: "relaying", stepReached: 2, instructionId } : s));

        const response = await pollResult(instructionId);
        const ok = response.result.status === 1;
        const decoded = decodeResultData(response.result.data);
        const resultJson = JSON.stringify(decoded ?? {}, null, 2);

        if (!ok) {
          setRun((s) =>
            s ? { ...s, phase: "error", resultJson, error: response.result.log || "instruction failed" } : s,
          );
          return;
        }

        // Optional on-chain post-step (withdraw: release TestUSDC on Coston).
        if (opts?.onSlip) {
          setRun((s) => (s ? { ...s, phase: "releasing", stepReached: 3, resultJson } : s));
          await opts.onSlip(decoded);
        }

        // Let the caller inspect the decoded result (e.g. detect selfFund).
        opts?.onDecoded?.(decoded);

        setRun((s) =>
          s ? { ...s, phase: "done", stepReached: opts?.onSlip ? 4 : 3, resultJson } : s,
        );

        // learn the derived HL address from a deposit result
        const addr =
          decoded && typeof decoded === "object"
            ? (decoded as { hlAddress?: unknown }).hlAddress
            : undefined;
        if (typeof addr === "string" && address) {
          setHlAddress(addr);
          localStorage.setItem(`hl:${address.toLowerCase()}`, addr);
        }
        setRefreshNonce((n) => n + 1);
        setTimeout(() => setRefreshNonce((n) => n + 1), 1800);
        dismissTimer.current = setTimeout(
          () => setRun((s) => (s && s.phase === "done" ? null : s)),
          5200,
        );
      } catch (e) {
        setRun((s) => (s ? { ...s, phase: "error", error: friendlyError(e) } : s));
      } finally {
        setPendingCoins(new Set());
      }
    },
    [publicClient, busy, address],
  );

  // Thin wrapper for JSON-payload instructions (trade / close / cancel).
  const send = useCallback(
    (fn: Fn, payload: unknown, meta: { kind: RunKind; title: string; sub: string }) =>
      runInstruction(
        () =>
          writeContractAsync({
            address: INSTRUCTION_SENDER,
            abi: instructionSenderAbi,
            functionName: fn,
            args: [stringToHex(JSON.stringify(payload))],
            value: INSTRUCTION_FEE_WEI,
            chainId: activeChain.id,
          }),
        meta,
      ),
    [runInstruction, writeContractAsync],
  );

  const vaultNotConfigured = (kind: RunKind, title: string) =>
    setRun({
      phase: "error",
      stepReached: 0,
      detailsOpen: false,
      kind,
      title,
      sub: "",
      error: "Vault not configured — set NEXT_PUBLIC_TESTUSDC and NEXT_PUBLIC_VAULT.",
    });

  // Deposit: faucet-mint any TestUSDC shortfall → approve → escrow to the vault.
  const doDeposit = useCallback(
    async (amt: number) => {
      if (busy || !publicClient || !address) return;
      const title = `Deposit ${usd(amt)}`;
      if (!VAULT_CONFIGURED) return vaultNotConfigured("deposit", title);
      const units = usdToUnits(amt);
      await runInstruction(
        async () => {
          const bal = (await publicClient.readContract({
            address: TESTUSDC,
            abi: testUsdcAbi,
            functionName: "balanceOf",
            args: [address],
          })) as bigint;
          if (bal < units) {
            const h = await writeContractAsync({
              address: TESTUSDC,
              abi: testUsdcAbi,
              functionName: "mint",
              args: [address, units - bal],
              chainId: activeChain.id,
            });
            await publicClient.waitForTransactionReceipt({ hash: h });
          }
          const allow = (await publicClient.readContract({
            address: TESTUSDC,
            abi: testUsdcAbi,
            functionName: "allowance",
            args: [address, INSTRUCTION_SENDER],
          })) as bigint;
          if (allow < units) {
            const h = await writeContractAsync({
              address: TESTUSDC,
              abi: testUsdcAbi,
              functionName: "approve",
              args: [INSTRUCTION_SENDER, units],
              chainId: activeChain.id,
            });
            await publicClient.waitForTransactionReceipt({ hash: h });
          }
          return writeContractAsync({
            address: INSTRUCTION_SENDER,
            abi: instructionSenderAbi,
            functionName: "deposit",
            args: [TESTUSDC, units],
            value: INSTRUCTION_FEE_WEI,
            chainId: activeChain.id,
          });
        },
        { kind: "deposit", title, sub: "Mint + approve TestUSDC, then escrow to the vault" },
        {
          onDecoded: (decoded) => {
            const d = decoded as { selfFund?: boolean; selfFundAddress?: string };
            if (d?.selfFund && d?.selfFundAddress) {
              setSelfFund({ address: d.selfFundAddress, amountUnits: units, amountUsd: amt });
            }
          },
        },
      );
    },
    [busy, publicClient, address, runInstruction, writeContractAsync],
  );

  // Withdraw: requestWithdraw (unwind on HL, TEE signs a slip) → executeWithdraw
  // on the vault to release the escrowed TestUSDC on Coston.
  const doWithdraw = useCallback(
    async (amt: number) => {
      if (busy || !publicClient) return;
      const title = `Withdraw ${usd(amt)}`;
      if (!VAULT_CONFIGURED) return vaultNotConfigured("withdraw", title);
      const units = usdToUnits(amt);
      await runInstruction(
        () =>
          writeContractAsync({
            address: INSTRUCTION_SENDER,
            abi: instructionSenderAbi,
            functionName: "requestWithdraw",
            args: [TESTUSDC, units],
            value: INSTRUCTION_FEE_WEI,
            chainId: activeChain.id,
          }),
        { kind: "withdraw", title, sub: "Unwind on HL, then release on Coston" },
        {
          onSlip: async (decoded) => {
            const d = decoded as {
              vault?: string;
              user?: string;
              token?: string;
              amount?: string;
              withdrawalId?: string;
              signature?: string;
            };
            if (!d?.signature || !d?.user || !d?.token || !d?.amount || !d?.withdrawalId) {
              throw new Error("no withdrawal slip returned by the TEE");
            }
            const h = await writeContractAsync({
              address: (d.vault || VAULT) as `0x${string}`,
              abi: vaultAbi,
              functionName: "executeWithdraw",
              args: [
                d.user as `0x${string}`,
                d.token as `0x${string}`,
                BigInt(d.amount),
                d.withdrawalId as `0x${string}`,
                d.signature as `0x${string}`,
              ],
              chainId: activeChain.id,
            });
            await publicClient.waitForTransactionReceipt({ hash: h });
          },
        },
      );
    },
    [busy, publicClient, runInstruction, writeContractAsync],
  );

  // Confirm self-funding: the user funded their HL address via the faucet and
  // now asks the TEE to verify the balance and credit the ledger.
  const doConfirmFunding = useCallback(async () => {
    if (busy || !publicClient || !selfFund) return;
    const title = `Confirm ${usd(selfFund.amountUsd)} funding`;
    await runInstruction(
      () =>
        writeContractAsync({
          address: INSTRUCTION_SENDER,
          abi: instructionSenderAbi,
          functionName: "confirmFunding",
          args: [TESTUSDC, selfFund.amountUnits],
          value: INSTRUCTION_FEE_WEI,
          chainId: activeChain.id,
        }),
      { kind: "deposit", title, sub: "Verifying your HL balance…" },
      {
        onDecoded: () => setSelfFund(null),
      },
    );
  }, [busy, publicClient, selfFund, runInstruction, writeContractAsync]);

  // ---- action handlers ----
  const onConnect = async () => {
    setConnectError(undefined);
    if (typeof window !== "undefined" && !("ethereum" in window)) {
      setHasWallet(false);
      return;
    }
    setConnecting(true);
    try {
      await connectAsync({ connector: injected() });
      setShowOnboarding(true);
    } catch (e) {
      setConnectError(friendlyError(e));
    } finally {
      setConnecting(false);
    }
  };
  const onDisconnect = () => {
    disconnect();
    setRun(null);
    setModal(null);
    setPendingCoins(new Set());
  };

  const openDeposit = () => setModal({ kind: "deposit", amount: "50" });
  const openWithdraw = () => setModal({ kind: "withdraw", amount: "" });
  const modalSetMax = () =>
    setModal((m) =>
      !m
        ? m
        : {
            ...m,
            amount:
              m.kind === "deposit"
                ? String(Math.max(0, MAX_DEPOSIT_USD - balance))
                : String(Math.floor(withdrawable * 100) / 100),
          },
    );

  const submitModal = () => {
    if (!modal || busy) return;
    const amt = num(modal.amount);
    if (modal.kind === "deposit") {
      if (amt <= 0 || balance + amt > MAX_DEPOSIT_USD) return;
      setModal(null);
      doDeposit(amt);
    } else {
      if (amt <= 0 || amt > withdrawable) return;
      setModal(null);
      doWithdraw(amt);
    }
  };

  const onTrade = (p: TradePayload) => {
    const d = szDecimals.current[p.coin] ?? marketBy(p.coin).szDecimals;
    const size = round(p.size, d);
    if (size <= 0) return;
    const payload: Record<string, unknown> = {
      market: "perp",
      coin: p.coin,
      isBuy: p.isBuy,
      orderType: p.orderType,
      size,
      leverage: p.leverage,
      reduceOnly: p.reduceOnly,
      isCross: true,
    };
    if (p.orderType === "limit" && p.limitPx) payload.limitPx = p.limitPx;
    send("placeOrder", payload, {
      kind: "trade",
      title: p.label,
      sub: `${usd(p.notional)} position`,
    });
    setPay("50");
  };

  const onClose = (coin: string, frac: number) => {
    if (busy) return;
    const p = account?.positions.find((x) => x.coin === coin);
    if (!p) return;
    const d = szDecimals.current[coin] ?? marketBy(coin).szDecimals;
    const full = Math.abs(p.szi);
    const size = frac >= 1 ? full : round(full * frac, d);
    if (size <= 0) return;
    const dir = p.szi >= 0 ? "Long" : "Short";
    setPendingCoins(new Set([coin]));
    // leverage:0 so the handler skips SetLeverage (HL rejects a leverage change
    // while a position is open); a reduce-only close keeps existing leverage.
    send(
      "placeOrder",
      {
        market: "perp",
        coin,
        isBuy: p.szi < 0,
        orderType: "market",
        size,
        reduceOnly: true,
        isCross: true,
        leverage: 0,
      },
      { kind: "close", title: `${frac < 1 ? "Close 50% " : "Close "}${coin} ${dir}`, sub: "Reduce-only market order" },
    );
  };

  const onCancel = (coin: string, oid: number) => {
    if (busy) return;
    send("cancel", { coin, orderId: oid }, {
      kind: "cancel",
      title: `Cancel ${coin} #${oid}`,
      sub: "Removing resting order",
    });
  };

  const walletAddr = useMemo(() => (address ? truncAddr(address) : ""), [address]);

  return (
    <div
      style={css(
        "min-height:100vh;background:radial-gradient(1200px 700px at 78% -10%, rgba(124,140,255,0.08), transparent 60%),#080a0f;color:#e9ecf4;font-family:var(--font-manrope),system-ui,sans-serif;font-feature-settings:'tnum' 1;-webkit-font-smoothing:antialiased;",
      )}
    >
      <Header
        connected={isConnected}
        wrongNetwork={wrongNetwork}
        balanceStr={usd(displayBalance)}
        walletAddr={walletAddr}
        flareAddress={address}
        hlAddress={hlAddress}
        onSwitch={() => switchChain({ chainId: activeChain.id })}
        onDeposit={openDeposit}
        onDisconnect={onDisconnect}
      />
      <Banner />

      {!isConnected && (
        <Hero
          onConnect={onConnect}
          connecting={connecting}
          error={connectError}
          hasWallet={hasWallet}
        />
      )}

      {isConnected && (
        <main style={css("max-width:1080px;margin:0 auto;padding:26px 24px 80px;")}>
          {showOnboarding && <Onboarding onDismiss={() => setShowOnboarding(false)} />}

          {selfFund && (
            <SelfFundPanel
              address={selfFund.address}
              amountUsd={selfFund.amountUsd}
              busy={busy}
              onConfirm={doConfirmFunding}
              onDismiss={() => setSelfFund(null)}
            />
          )}

          {address && <AdminPanel address={address} />}
          <div style={css("display:grid;grid-template-columns:minmax(0,1fr) 380px;gap:22px;align-items:start;")}>
            <TradeCard
              market={market}
              mids={mids}
              marketOpen={marketOpen}
              onToggleMarket={() => setMarketOpen((v) => !v)}
              onPickMarket={(sym) => {
                setMarket(sym);
                setMarketOpen(false);
              }}
              direction={direction}
              onLong={() => setDirection("long")}
              onShort={() => setDirection("short")}
              pay={pay}
              onPay={setPay}
              balance={balance}
              available={available}
              existingPosition={existingPosition}
              onMax={() => setPay(String(Math.floor(available * 100) / 100))}
              leverage={leverage}
              onLev={setLeverage}
              advancedOpen={advancedOpen}
              onToggleAdvanced={() => setAdvancedOpen((v) => !v)}
              useLimit={useLimit}
              onToggleLimit={() =>
                setUseLimit((v) => {
                  if (!v && mids[market]) setLimitPrice(mids[market].toFixed(2));
                  return !v;
                })
              }
              limitPrice={limitPrice}
              onLimit={setLimitPrice}
              reduceOnly={reduceOnly}
              onToggleReduce={() => setReduceOnly((v) => !v)}
              locked={!everDeposited}
              busy={busy}
              onDeposit={openDeposit}
              onSubmit={onTrade}
            />

            <AccountColumn
              balance={displayBalance}
              available={available}
              inPositions={inPositions}
              everDeposited={everDeposited}
              positions={account?.positions ?? []}
              orders={account?.openOrders ?? []}
              mids={mids}
              pendingCoins={pendingCoins}
              busy={busy}
              onDeposit={openDeposit}
              onWithdraw={openWithdraw}
              onClose={onClose}
              onCancel={onCancel}
            />
          </div>
        </main>
      )}

      {modal && (
        <TxModal
          kind={modal.kind}
          amount={modal.amount}
          balance={balance}
          withdrawable={withdrawable}
          maxDeposit={MAX_DEPOSIT_USD}
          busy={busy}
          onAmount={(v) => setModal((m) => (m ? { ...m, amount: v } : m))}
          onMax={modalSetMax}
          onPick={(v) => setModal((m) => (m ? { ...m, amount: v } : m))}
          onClose={() => setModal(null)}
          onSubmit={submitModal}
        />
      )}

      {run && (
        <StatusToast
          run={run}
          onToggleDetails={() => setRun((s) => (s ? { ...s, detailsOpen: !s.detailsOpen } : s))}
          onDismiss={() => setRun(null)}
        />
      )}
    </div>
  );
}
